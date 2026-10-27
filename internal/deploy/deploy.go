package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/health"
	"github.com/behaviorengineering/polypus/internal/deploy/images"
	"github.com/behaviorengineering/polypus/internal/deploy/overlay"
	"github.com/behaviorengineering/polypus/internal/deploy/paths"
	"github.com/behaviorengineering/polypus/internal/deploy/routers"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
)

// bindMountReloadServices are force-recreated after compose up so bind-mounted
// files (gateway YAML, otelcol config, Tailscale serve.json) reload on Windows.
var bindMountReloadServices = []string{"gateway", "otelcol", "tailscale"}

// plan is what a deploy resolves before it changes anything.
type plan struct {
	gatewayPath    string
	imagesEnv      string
	pins           string // resolved pins to write to imagesEnv; empty when an environment file is used.
	routers        bool
	smokeSystemOne bool
	secretNames    []string
}

// Run deploys the environment in opts: resolve the plan, then (unless DryRun)
// write the pins and secrets, start the stack, and check health and smoke.
func Run(ctx context.Context, opts Options) error {
	if ctx == nil {
		return fmt.Errorf("deploy: context required")
	}
	p, err := resolvePlan(opts)
	if err != nil {
		return err
	}
	printPlan(opts, p)
	if opts.DryRun {
		fmt.Println("deploy: dry run; nothing written, no containers changed")
		return nil
	}
	return apply(ctx, opts, p)
}

func resolvePlan(opts Options) (plan, error) {
	var p plan
	gatewayPath, err := paths.ResolveGatewayConfig(opts.GatewayConfig)
	if err != nil {
		return p, err
	}
	p.gatewayPath = gatewayPath
	if p.imagesEnv, p.pins, err = resolvePins(opts); err != nil {
		return p, err
	}
	if p.routers, err = routers.HasTopLevelRouters(gatewayPath); err != nil {
		return p, fmt.Errorf("deploy: routers detect: %w", err)
	}
	if p.smokeSystemOne, err = smokeSystemOne(gatewayPath); err != nil {
		return p, err
	}
	if p.secretNames, err = ExportSecretNames(opts); err != nil {
		return p, fmt.Errorf("deploy: gateway secrets: %w", err)
	}
	otelCfg := filepath.Join(opts.Root, "otelcol.config.yaml")
	if _, err := os.Stat(otelCfg); err != nil {
		return p, fmt.Errorf("deploy: missing otel collector config at %s", otelCfg)
	}
	return p, nil
}

// resolvePins returns the images file compose reads and, when the deploy writes
// that file itself, its content. An environment's own pins file is used as is.
func resolvePins(opts Options) (path, pins string, err error) {
	if strings.TrimSpace(opts.PinsFile) != "" {
		if err := images.Verify(opts.PinsFile); err != nil {
			return "", "", err
		}
		return opts.PinsFile, "", nil
	}
	pins, err = images.VersionPins(opts.RepoPins, opts.Version)
	if err != nil {
		return "", "", err
	}
	if err := images.VerifyPins(pins, opts.RepoPins); err != nil {
		return "", "", err
	}
	return opts.ImagesEnv, pins, nil
}

func smokeSystemOne(gatewayPath string) (bool, error) {
	switch os.Getenv("POLYPUS_SMOKE_SYSTEMONE") {
	case "0":
		return false, nil
	case "1":
		return true, nil
	}
	enabled, err := gatewayconfig.SystemOneBackendEnabled(gatewayPath)
	if err != nil {
		return false, fmt.Errorf("deploy: systemone config: %w", err)
	}
	return enabled, nil
}

func printPlan(opts Options, p plan) {
	if opts.Environment.Path != "" {
		fmt.Printf("deploy: environment %s (%s)\n", opts.Environment.Identifier, opts.Environment.Path)
	}
	fmt.Printf("deploy: gateway config %s\n", p.gatewayPath)
	if p.pins == "" {
		fmt.Printf("deploy: images %s\n", p.imagesEnv)
	} else {
		fmt.Printf("deploy: images of release %s (written to %s)\n", opts.Version, p.imagesEnv)
	}
	if p.routers {
		fmt.Println("deploy: routers in host config; COMPOSE_PROFILES=routers POLYPUS_SWITCHYARD=1")
	} else {
		fmt.Println("deploy: no routers in host config; switchyard profile off")
	}
	fmt.Printf("deploy: secrets %s\n", strings.Join(p.secretNames, ","))
}

func apply(ctx context.Context, opts Options, p plan) error {
	if p.pins != "" {
		if err := images.WritePins(p.imagesEnv, p.pins); err != nil {
			return err
		}
	}
	if err := images.EnsurePresent(ctx, p.imagesEnv, images.EnsureOptions{
		Wait:        true,
		WaitTimeout: 20 * time.Minute,
	}); err != nil {
		return err
	}
	if err := setComposeEnv(p); err != nil {
		return err
	}
	if p.smokeSystemOne {
		if err := printSystemOneTargets(p.gatewayPath); err != nil {
			return err
		}
	}
	if err := secrets.Export(opts.App, p.secretNames, opts.EnvDeploy); err != nil {
		return err
	}
	fmt.Printf("export-env: wrote %s\n", opts.EnvDeploy)

	cc := compose.Client{
		ComposeFile: opts.ComposeFile,
		EnvFiles:    []string{p.imagesEnv, opts.EnvDeploy},
	}
	if err := startStack(ctx, opts, p, cc); err != nil {
		health.DumpGateway()
		return err
	}
	if err := health.Poll(ctx, opts.HealthURL, opts.HealthTimeout); err != nil {
		health.DumpGateway()
		return err
	}
	if opts.SkipSmoke {
		fmt.Println("deploy: skip smoke (--skip-smoke)")
		fmt.Printf("deploy ok: %s/health\n", opts.HealthURL)
		return nil
	}
	if err := runSmoke(ctx, opts, p); err != nil {
		health.DumpGateway()
		return fmt.Errorf("deploy: smoke: %w", err)
	}
	fmt.Printf("deploy ok: %s/health (smoke passed)\n", opts.HealthURL)
	return nil
}

func setComposeEnv(p plan) error {
	if err := setProcessEnv("POLYPUS_HOST_CONFIG", paths.DockerBindPath(p.gatewayPath)); err != nil {
		return err
	}
	if !p.routers {
		if err := setProcessEnv("POLYPUS_SWITCHYARD", "0"); err != nil {
			return err
		}
		return unsetProcessEnv("COMPOSE_PROFILES")
	}
	if err := setProcessEnv("POLYPUS_SWITCHYARD", "1"); err != nil {
		return err
	}
	return setProcessEnv("COMPOSE_PROFILES", "routers")
}

func printSystemOneTargets(gatewayPath string) error {
	fmt.Println("deploy: systemone smoke enabled")
	models, err := gatewayconfig.SystemOneSmokeTargets(gatewayPath)
	if err != nil {
		return fmt.Errorf("deploy: systemone smoke targets: %w", err)
	}
	for _, t := range models {
		fmt.Printf("deploy: systemone smoke model=%s\n", t.Model)
	}
	return nil
}

func startStack(ctx context.Context, opts Options, p plan, cc compose.Client) error {
	if !opts.SkipPull {
		if err := cc.Pull(ctx); err != nil {
			return err
		}
	}
	if err := cc.Up(ctx); err != nil {
		return err
	}
	fmt.Println("deploy: recreate gateway, otelcol, and tailscale (reload bind-mounted config)")
	return postComposeUpReload(ctx, opts, p.gatewayPath, cc.RecreateServices, bindMountReloadServices...)
}

func runSmoke(ctx context.Context, opts Options, p plan) error {
	smokeTimeout := opts.SmokeTimeout
	if smokeTimeout <= 0 {
		smokeTimeout = smoke.DefaultSmokeTimeout
	}
	smokeCtx, smokeCancel := context.WithTimeout(ctx, smokeTimeout)
	defer smokeCancel()
	withProviders := os.Getenv("POLYPUS_SMOKE_PROVIDERS") != "0"
	if withProviders {
		fmt.Println("deploy: provider chat smoke enabled (POLYPUS_SMOKE_PROVIDERS=0 to disable)")
	}
	return smoke.Run(smokeCtx, smoke.Options{
		BaseURL:       opts.HealthURL,
		ChatModel:     opts.SmokeChatModel,
		RouterModel:   opts.SmokeRouterModel,
		Capable:       opts.SmokeCapable,
		Efficient:     opts.SmokeEfficient,
		WithRouter:    p.routers,
		WithSystemOne: p.smokeSystemOne,
		WithProviders: withProviders,
		GatewayConfig: p.gatewayPath,
		EnvDeploy:     opts.EnvDeploy,
	})
}

func postComposeUpReload(
	ctx context.Context,
	opts Options,
	gatewayPath string,
	recreate func(context.Context, ...string) error,
	services ...string,
) error {
	if err := runPostUpOverlayPrune(ctx, opts, gatewayPath); err != nil {
		return err
	}
	return recreate(ctx, services...)
}

func runPostUpOverlayPrune(ctx context.Context, opts Options, gatewayPath string) error {
	if opts.SkipOverlayPrune {
		fmt.Println("deploy: skip overlay prune (--skip-overlay-prune)")
		return nil
	}
	prune := opts.PruneFunc
	if prune == nil {
		prune = overlay.PruneContainerDefault
	}
	if err := prune(ctx, gatewayPath); err != nil {
		return fmt.Errorf("deploy: overlay prune: %w", err)
	}
	return nil
}
