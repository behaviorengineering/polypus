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
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	"github.com/behaviorengineering/polypus/internal/deploy/tailnet"
	"github.com/behaviorengineering/polypus/internal/deploy/tsserve"
)

// observabilityServices run behind the observability compose profile.
var observabilityServices = []string{"otelcol", "phoenix", "hyperdx"}

// collectorEndpoint is where the gateway and Switchyard send traces when the
// collector runs.
const collectorEndpoint = "http://otelcol:4317"

// reloadServices are force-recreated after compose up so bind-mounted files
// (gateway YAML, otelcol config, Tailscale serve.json) reload on Windows. Only
// services that run are listed.
func reloadServices(svc images.Services) []string {
	if svc.Observability {
		return []string{"gateway", "otelcol", "tailscale"}
	}
	return []string{"gateway", "tailscale"}
}

// plan is what a deploy resolves before it changes anything.
type plan struct {
	gatewayPath    string
	imagesEnv      string
	pins           string // resolved pins to write to imagesEnv; empty when an environment file is used.
	services       images.Services
	smokeRouters   bool // the gateway YAML has routers: (passthrough ones need no Switchyard).
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
	if p.services, err = Services(opts); err != nil {
		return p, err
	}
	if p.smokeRouters, err = routers.HasTopLevelRouters(gatewayPath); err != nil {
		return p, fmt.Errorf("deploy: routers detect: %w", err)
	}
	if p.imagesEnv, p.pins, err = resolvePins(opts, p.services); err != nil {
		return p, err
	}
	if p.smokeSystemOne, err = smokeSystemOne(gatewayPath); err != nil {
		return p, err
	}
	if p.secretNames, err = ExportSecretNames(opts); err != nil {
		return p, fmt.Errorf("deploy: gateway secrets: %w", err)
	}
	return p, nil
}

// Services returns the optional services the environment runs: Switchyard when its
// gateway YAML has a composed router, and the observability stack when its switch is on.
func Services(opts Options) (images.Services, error) {
	gatewayPath, err := paths.ResolveGatewayConfig(opts.GatewayConfig)
	if err != nil {
		return images.Services{}, err
	}
	composed, err := gatewayconfig.HasComposedRouters(gatewayPath)
	if err != nil {
		return images.Services{}, fmt.Errorf("deploy: routers detect: %w", err)
	}
	return images.Services{Routers: composed, Observability: opts.Environment.Observability}, nil
}

// resolvePins returns the images file compose reads and, when the deploy writes
// that file itself, its content. An environment's own pins file is used as is.
func resolvePins(opts Options, svc images.Services) (path, pins string, err error) {
	if strings.TrimSpace(opts.PinsFile) != "" {
		if err := images.Verify(opts.PinsFile, svc); err != nil {
			return "", "", err
		}
		return opts.PinsFile, "", nil
	}
	pins, err = images.VersionPins(stack.ImagePins(), opts.Version)
	if err != nil {
		return "", "", err
	}
	if err := images.VerifyPins(pins, "the embedded images.env", svc); err != nil {
		return "", "", err
	}
	return opts.ImagesEnvPath(), pins, nil
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
	fmt.Printf("deploy: compose project, tailnet host and keyring service %s\n", opts.Name)
	fmt.Printf("deploy: gateway config %s\n", p.gatewayPath)
	fmt.Printf("deploy: writes the stack, pins and secrets to %s\n", opts.Dir())
	fmt.Printf("deploy: health and smoke at %s\n", opts.HealthURL)
	if opts.Tailnet != "" {
		fmt.Printf("deploy: checks this machine is on tailnet %s first\n", opts.Tailnet)
	}
	if p.pins == "" {
		fmt.Printf("deploy: images %s\n", p.imagesEnv)
	} else {
		fmt.Printf("deploy: images of release %s (written to %s)\n", opts.Version, p.imagesEnv)
	}
	switch {
	case p.services.Routers:
		fmt.Println("deploy: composed routers in host config; switchyard on")
	case p.smokeRouters:
		fmt.Println("deploy: passthrough routers only; the gateway serves them, switchyard off")
	default:
		fmt.Println("deploy: no routers in host config; switchyard off")
	}
	if p.services.Observability {
		fmt.Println("deploy: observability on; collector, Phoenix and HyperDX run")
	} else {
		fmt.Println("deploy: observability off; collector, Phoenix and HyperDX do not run")
	}
	fmt.Printf("deploy: secrets %s\n", strings.Join(p.secretNames, ","))
}

func apply(ctx context.Context, opts Options, p plan) error {
	if opts.Tailnet != "" {
		if err := tailnet.Check(ctx, opts.Tailnet, opts.TailnetStatus); err != nil {
			return fmt.Errorf("deploy: %w", err)
		}
		fmt.Printf("deploy: this machine is on tailnet %s\n", opts.Tailnet)
	}
	composeFile, err := stack.Write(opts.Dir())
	if err != nil {
		return err
	}
	if p.pins != "" {
		if err := images.WritePins(p.imagesEnv, p.pins); err != nil {
			return err
		}
	}
	if err := images.EnsurePresent(ctx, p.imagesEnv, images.EnsureOptions{
		Wait:        true,
		WaitTimeout: 20 * time.Minute,
		Services:    p.services,
	}); err != nil {
		return err
	}
	serveDir := filepath.Join(opts.Dir(), "tailscale")
	servePath, err := tsserve.Write(serveDir, p.services.Observability)
	if err != nil {
		return err
	}
	fmt.Printf("deploy: wrote %s\n", servePath)
	if err := setComposeEnv(opts, p, serveDir); err != nil {
		return err
	}
	if p.smokeSystemOne {
		if err := printSystemOneTargets(p.gatewayPath); err != nil {
			return err
		}
	}
	if err := secrets.Export(opts.Name, p.secretNames, opts.EnvDeployPath()); err != nil {
		return fmt.Errorf("%w (store each with: polypus deploy secret set <ENV>)", err)
	}
	fmt.Printf("export-env: wrote %s\n", opts.EnvDeployPath())

	cc := compose.Client{
		Project:     opts.Name,
		ComposeFile: composeFile,
		EnvFiles:    []string{p.imagesEnv, opts.EnvDeployPath()},
	}
	if err := startStack(ctx, opts, p, cc); err != nil {
		health.DumpGateway(cc)
		return err
	}
	if err := health.Poll(ctx, opts.HealthURL, opts.HealthTimeout); err != nil {
		health.DumpGateway(cc)
		return err
	}
	if opts.SkipSmoke {
		fmt.Println("deploy: skip smoke (--skip-smoke)")
		fmt.Printf("deploy ok: %s/health\n", opts.HealthURL)
		return nil
	}
	if err := runSmoke(ctx, opts, p); err != nil {
		health.DumpGateway(cc)
		return fmt.Errorf("deploy: smoke: %w", err)
	}
	fmt.Printf("deploy ok: %s/health (smoke passed)\n", opts.HealthURL)
	return nil
}

// setComposeEnv sets the variables docker-compose.deploy.yml reads from the deploy.
func setComposeEnv(opts Options, p plan, serveDir string) error {
	switchyard, endpoint := "0", ""
	if p.services.Routers {
		switchyard = "1"
	}
	if p.services.Observability {
		endpoint = collectorEndpoint
	}
	vars := [][2]string{
		{"POLYPUS_HOST_CONFIG", paths.DockerBindPath(p.gatewayPath)},
		{"POLYPUS_TS_HOSTNAME", opts.Name},
		{"POLYPUS_TS_SERVE_DIR", paths.DockerBindPath(serveDir)},
		{"POLYPUS_SWITCHYARD", switchyard},
		{"POLYPUS_DEPLOY_OTLP_ENDPOINT", endpoint},
	}
	for _, v := range vars {
		if err := setProcessEnv(v[0], v[1]); err != nil {
			return err
		}
	}
	if profiles := composeProfiles(p.services); profiles != "" {
		return setProcessEnv("COMPOSE_PROFILES", profiles)
	}
	return unsetProcessEnv("COMPOSE_PROFILES")
}

// composeProfiles is the COMPOSE_PROFILES value for the services that run.
func composeProfiles(svc images.Services) string {
	var profiles []string
	if svc.Routers {
		profiles = append(profiles, "routers")
	}
	if svc.Observability {
		profiles = append(profiles, "observability")
	}
	return strings.Join(profiles, ",")
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
	if !p.services.Routers {
		// compose up leaves a profile's containers running once the profile is off.
		fmt.Println("deploy: remove the switchyard container if present")
		if err := cc.RemoveServices(ctx, "switchyard"); err != nil {
			return err
		}
	}
	if !p.services.Observability {
		// compose up leaves a profile's containers running once the profile is off.
		fmt.Println("deploy: remove collector, Phoenix and HyperDX containers if present (their volumes stay)")
		if err := cc.RemoveServices(ctx, observabilityServices...); err != nil {
			return err
		}
	}
	reload := reloadServices(p.services)
	fmt.Printf("deploy: recreate %s (reload bind-mounted config)\n", strings.Join(reload, ", "))
	if opts.PruneFunc == nil {
		opts.PruneFunc = func(ctx context.Context, gatewayPath string) error {
			return overlay.PruneContainer(ctx, gatewayPath, overlay.ContainerConfig{Compose: cc})
		}
	}
	return postComposeUpReload(ctx, opts, p.gatewayPath, cc.RecreateServices, reload...)
}

func runSmoke(ctx context.Context, opts Options, p plan) error {
	smokeTimeout := opts.SmokeTimeout
	if smokeTimeout <= 0 {
		smokeTimeout = smoke.DefaultSmokeTimeout
	}
	smokeCtx, smokeCancel := context.WithTimeout(ctx, smokeTimeout)
	defer smokeCancel()
	clientKey, err := secrets.Lookup(opts.Keyring, opts.Name, secrets.ClientKeyName)
	if err != nil {
		return err
	}
	if clientKey != "" {
		fmt.Printf("deploy: smoke sends the client key (%s)\n", secrets.ClientKeyName)
	}
	withProviders := os.Getenv("POLYPUS_SMOKE_PROVIDERS") != "0"
	if withProviders {
		fmt.Println("deploy: provider chat smoke enabled (POLYPUS_SMOKE_PROVIDERS=0 to disable)")
	}
	return smoke.Run(smokeCtx, smoke.Options{
		BaseURL:       opts.HealthURL,
		ChatModel:     opts.SmokeChatModel,
		WithRouter:    p.smokeRouters,
		WithSystemOne: p.smokeSystemOne,
		WithProviders: withProviders,
		GatewayConfig: p.gatewayPath,
		EnvDeploy:     opts.EnvDeployPath(),
		APIKey:        clientKey,
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
	if opts.PruneFunc == nil {
		return fmt.Errorf("deploy: overlay prune: no prune function")
	}
	if err := opts.PruneFunc(ctx, gatewayPath); err != nil {
		return fmt.Errorf("deploy: overlay prune: %w", err)
	}
	return nil
}
