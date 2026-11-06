// Package deploy deploys one Polypus environment with Docker Compose: it resolves
// the plan from the environment's config and gateway YAML, writes the stack, pins
// and secrets, starts the containers, then checks health and smoke.
package deploy

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/health"
	"github.com/behaviorengineering/polypus/internal/deploy/images"
	"github.com/behaviorengineering/polypus/internal/deploy/overlay"
	"github.com/behaviorengineering/polypus/internal/deploy/paths"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	"github.com/behaviorengineering/polypus/internal/deploy/tailnet"
	"github.com/behaviorengineering/polypus/internal/deploy/tsserve"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// observabilityServices run behind the observability compose profile.
var observabilityServices = []string{stack.ServiceOtelcol, stack.ServicePhoenix, stack.ServiceHyperDX}

// collectorEndpoint is where the gateway and Switchyard send traces when the
// collector runs.
const collectorEndpoint = "http://otelcol:4317"

// healthProbeTimeout bounds one health GET, so a hung connection costs one round of
// the poll, not the whole health budget.
const healthProbeTimeout = 5 * time.Second

// reloadServices are force-recreated after compose up so bind-mounted files
// (gateway YAML, otelcol config, Tailscale serve.json) reload on Windows. Only
// services that run are listed.
func reloadServices(svc images.Services) []string {
	if svc.Observability {
		return []string{stack.ServiceGateway, stack.ServiceOtelcol, stack.ServiceTailscale}
	}
	return []string{stack.ServiceGateway, stack.ServiceTailscale}
}

// plan is what a deploy resolves before it changes anything.
type plan struct {
	gatewayPath string
	imagesEnv   string
	pins        string // resolved pins to write to imagesEnv; empty when an environment file is used.
	services    images.Services
	checks      SmokeChecks
	secretNames []string
}

// SmokeChecks are the probes a deploy runs after health. polypus deploy smoke
// repeats them, so a re-test only reaches backends the environment has keys for.
type SmokeChecks struct {
	Providers bool // one chat per backend in the gateway YAML whose secrets are set.
	Routers   bool // the gateway YAML has routers: (passthrough ones need no Switchyard).
	SystemOne bool
}

// ResolveSmokeChecks returns the smoke checks for the gateway YAML at gatewayPath.
// POLYPUS_SMOKE_PROVIDERS=0 and POLYPUS_SMOKE_SYSTEMONE=0|1 override the YAML.
func ResolveSmokeChecks(gatewayPath string) (SmokeChecks, error) {
	const op = "deploy.ResolveSmokeChecks"
	c := SmokeChecks{Providers: os.Getenv("POLYPUS_SMOKE_PROVIDERS") != "0"}
	var err error
	if c.Routers, err = gatewayconfig.HasRouters(gatewayPath); err != nil {
		return c, derrors.Wrap(err, derrors.CodeInvalid, op, "routers detect")
	}
	if c.SystemOne, err = smokeSystemOne(gatewayPath); err != nil {
		return c, derrors.Wrap(err, derrors.CodeInvalid, op, "systemone config")
	}
	return c, nil
}

// Run deploys the environment in opts: resolve the plan, then (unless DryRun)
// write the pins and secrets, start the stack, and check health and smoke.
func Run(ctx context.Context, opts Options) error {
	const op = "deploy.Run"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	p, err := resolvePlan(opts)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "resolve plan").With("environment", opts.Name)
	}
	out := progress.New(opts.Out)
	printPlan(out, opts, p)
	if opts.DryRun {
		out.Println("deploy: dry run; nothing written, no containers changed")
		return out.Err()
	}
	if err := apply(ctx, opts, p, out); err != nil {
		return err
	}
	return out.Err()
}

func resolvePlan(opts Options) (plan, error) {
	var p plan
	gatewayPath, err := paths.ResolveGatewayConfig(opts.GatewayConfig)
	if err != nil {
		return p, err
	}
	p.gatewayPath = gatewayPath
	if err := gatewayconfig.RequireBackends(gatewayPath); err != nil {
		return p, derrors.Wrap(err, derrors.CodeInvalid, "deploy.resolvePlan", "gateway config")
	}
	if p.services, err = Services(opts); err != nil {
		return p, err
	}
	if p.checks, err = ResolveSmokeChecks(gatewayPath); err != nil {
		return p, err
	}
	if p.imagesEnv, p.pins, err = resolvePins(opts, p.services); err != nil {
		return p, err
	}
	if p.secretNames, err = ExportSecretNames(opts); err != nil {
		return p, derrors.Wrap(err, derrors.CodeInvalid, "deploy.resolvePlan", "gateway secrets")
	}
	return p, nil
}

// Services returns the optional services the environment runs: Switchyard when its
// gateway YAML has a composed router, and the observability stack when its switch is on.
func Services(opts Options) (images.Services, error) {
	gatewayPath, err := paths.ResolveGatewayConfig(opts.GatewayConfig)
	if err != nil {
		return images.Services{}, derrors.Wrap(err, derrors.CodeInvalid, "deploy.Services", "gateway config")
	}
	composed, err := gatewayconfig.HasComposedRouters(gatewayPath)
	if err != nil {
		return images.Services{}, derrors.Wrap(err, derrors.CodeInvalid, "deploy.Services", "routers detect")
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
	return gatewayconfig.SystemOneBackendEnabled(gatewayPath)
}

func printPlan(out *progress.Printer, opts Options, p plan) {
	if opts.Environment.Path != "" {
		out.Printf("deploy: environment %s (%s)\n", opts.Environment.Identifier, opts.Environment.Path)
	}
	out.Printf("deploy: compose project, tailnet host and keyring service %s\n", opts.Name)
	out.Printf("deploy: gateway config %s\n", p.gatewayPath)
	out.Printf("deploy: writes the stack, pins and secrets to %s\n", opts.Dir())
	out.Printf("deploy: health and smoke at %s\n", opts.HealthURL)
	if opts.Tailnet != "" {
		out.Printf("deploy: checks this machine is on tailnet %s first\n", opts.Tailnet)
	}
	if p.pins == "" {
		out.Printf("deploy: images %s\n", p.imagesEnv)
	} else {
		out.Printf("deploy: images of release %s (written to %s)\n", opts.Version, p.imagesEnv)
	}
	switch {
	case p.services.Routers:
		out.Println("deploy: composed routers in host config; switchyard on")
	case p.checks.Routers:
		out.Println("deploy: passthrough routers only; the gateway serves them, switchyard off")
	default:
		out.Println("deploy: no routers in host config; switchyard off")
	}
	if p.services.Observability {
		out.Println("deploy: observability on; collector, Phoenix and HyperDX run")
	} else {
		out.Println("deploy: observability off; collector, Phoenix and HyperDX do not run")
	}
	out.Printf("deploy: secrets %s\n", strings.Join(p.secretNames, ","))
}

// phase runs fn under a child span named deploy.<name>, and on failure logs one
// record with the environment and phase, then wraps the error with the phase.
func phase(ctx context.Context, opts Options, name string, fn func(context.Context) error) error {
	ctx, span := observability.Tracer().Start(ctx, "deploy."+name,
		trace.WithAttributes(attribute.String("deploy.environment", opts.Name)))
	err := fn(ctx)
	endSpan(span, err)
	if err == nil {
		return nil
	}
	opts.logger().ErrorContext(ctx, "deploy phase failed", "environment", opts.Name, "phase", name, "error", err)
	return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), "deploy."+name, name+" failed").
		With("environment", opts.Name)
}

// endSpan records err on span and ends it.
func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}

func apply(ctx context.Context, opts Options, p plan, out *progress.Printer) (err error) {
	ctx, span := observability.Tracer().Start(ctx, "deploy.apply",
		trace.WithAttributes(attribute.String("deploy.environment", opts.Name)))
	defer func() { endSpan(span, err) }()
	if opts.HealthTimeout <= 0 {
		err = derrors.New(derrors.CodeInvalid, "deploy.apply", "health timeout must be positive")
		return err
	}
	if opts.Tailnet != "" {
		if err = phase(ctx, opts, "tailnet.check", func(ctx context.Context) error {
			return tailnet.Check(ctx, opts.Tailnet, opts.TailnetStatus)
		}); err != nil {
			return err
		}
		out.Printf("deploy: this machine is on tailnet %s\n", opts.Tailnet)
	}
	var cc compose.Client
	if err = phase(ctx, opts, "prepare", func(ctx context.Context) error {
		var prepErr error
		cc, prepErr = prepare(ctx, opts, p, out)
		return prepErr
	}); err != nil {
		return err
	}
	if err = startStack(ctx, opts, p, cc, out); err != nil {
		err = withDiagnostics(ctx, opts, cc, err)
		return err
	}
	if err = phase(ctx, opts, "health.poll", func(ctx context.Context) error {
		pollCtx, cancel := context.WithTimeout(ctx, opts.HealthTimeout)
		defer cancel()
		return health.Poll(pollCtx, &http.Client{Timeout: healthProbeTimeout}, opts.HealthURL, opts.HealthPollInterval)
	}); err != nil {
		err = withDiagnostics(ctx, opts, cc, err)
		return err
	}
	hintIfKeyless(ctx, opts, out)
	if opts.SkipSmoke {
		out.Println("deploy: skip smoke (--skip-smoke)")
		out.Printf("deploy ok: %s/health\n", opts.HealthURL)
		return nil
	}
	if err = phase(ctx, opts, "smoke.run", func(ctx context.Context) error {
		return runSmoke(ctx, opts, p, out)
	}); err != nil {
		err = withDiagnostics(ctx, opts, cc, err)
		return err
	}
	out.Printf("deploy ok: %s/health (smoke passed)\n", opts.HealthURL)
	return nil
}

// prepare writes the stack, pins, serve config and secrets, checks the images, and
// returns the compose client for the environment.
func prepare(ctx context.Context, opts Options, p plan, out *progress.Printer) (compose.Client, error) {
	composeFile, err := stack.Write(opts.Dir())
	if err != nil {
		return compose.Client{}, err
	}
	if p.pins != "" {
		if err := images.WritePins(p.imagesEnv, p.pins); err != nil {
			return compose.Client{}, err
		}
	}
	if err := phase(ctx, opts, "images.ensure", func(ctx context.Context) error {
		ensure := images.DefaultEnsureOptions()
		if opts.ImageWaitTimeout > 0 {
			ensure.WaitTimeout = opts.ImageWaitTimeout
		}
		ensure.Services = p.services
		ensure.Out = opts.Out
		ensure.Probe = opts.ImageProbe
		return images.EnsurePresent(ctx, p.imagesEnv, ensure)
	}); err != nil {
		return compose.Client{}, err
	}
	serveDir := filepath.Join(opts.Dir(), stack.ServiceTailscale)
	servePath, err := tsserve.Write(serveDir, p.services.Observability)
	if err != nil {
		return compose.Client{}, err
	}
	out.Printf("deploy: wrote %s\n", servePath)
	if p.checks.SystemOne {
		if err := printSystemOneTargets(out, p.gatewayPath); err != nil {
			return compose.Client{}, err
		}
	}
	if err := secrets.ExportWithKeyring(opts.Keyring, opts.Name, p.secretNames, opts.EnvDeployPath()); err != nil {
		return compose.Client{}, err
	}
	out.Printf("export-env: wrote %s and %s beside it\n", opts.EnvDeployPath(), secrets.GatewayEnvFileName)
	return compose.Client{
		Project:     opts.Name,
		ComposeFile: composeFile,
		EnvFiles:    []string{p.imagesEnv, opts.EnvDeployPath()},
		Env:         composeEnv(opts, p, serveDir),
		Run:         opts.DockerRun,
		Output:      opts.DockerOutput,
		LookPath:    opts.DockerLookPath,
	}, nil
}

// keylessHint tells the operator how to lock a gateway that answers without a key.
const keylessHint = "deploy: the gateway has no access keys; anyone who can reach it (every member of the tailnet) can use it, including admin routes. Lock it: polypus deploy client-key"

// hintIfKeyless prints keylessHint when the deployed gateway answers its admin
// allow list without a key. The CLI cannot read the container's key store, so it
// asks the gateway. The check is advice only: when it cannot run, the deploy says
// so and carries on.
func hintIfKeyless(ctx context.Context, opts Options, out *progress.Printer) {
	ctx, cancel := context.WithTimeout(ctx, healthProbeTimeout)
	defer cancel()
	open, err := health.Keyless(ctx, &http.Client{Timeout: healthProbeTimeout}, opts.HealthURL)
	if err != nil {
		out.Printf("deploy: access key check skipped: %v\n", err)
		return
	}
	if open {
		out.Println(keylessHint)
	}
}

// withDiagnostics dumps the gateway's logs and state after a failure and returns
// err, joined with any failure to write the dump.
func withDiagnostics(ctx context.Context, opts Options, cc compose.Client, err error) error {
	if dumpErr := health.DumpGateway(ctx, opts.Out, cc); dumpErr != nil {
		return errors.Join(err, dumpErr)
	}
	return err
}

// composeEnv is the variables docker-compose.deploy.yml reads from the deploy. It
// is passed to each docker command, never set in this process's environment.
func composeEnv(opts Options, p plan, serveDir string) []string {
	switchyard, endpoint := "0", ""
	if p.services.Routers {
		switchyard = "1"
	}
	if p.services.Observability {
		endpoint = collectorEndpoint
	}
	return []string{
		"POLYPUS_HOST_CONFIG=" + paths.DockerBindPath(p.gatewayPath),
		"POLYPUS_TS_HOSTNAME=" + opts.Name,
		"POLYPUS_TS_SERVE_DIR=" + paths.DockerBindPath(serveDir),
		"POLYPUS_SWITCHYARD=" + switchyard,
		"POLYPUS_DEPLOY_OTLP_ENDPOINT=" + endpoint,
		// Always set, so an operator's own COMPOSE_PROFILES never leaks in; empty
		// runs no optional profile.
		"COMPOSE_PROFILES=" + composeProfiles(p.services),
	}
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

func printSystemOneTargets(out *progress.Printer, gatewayPath string) error {
	out.Println("deploy: systemone smoke enabled")
	models, err := gatewayconfig.SystemOneSmokeTargets(gatewayPath)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, "deploy.apply", "systemone smoke targets")
	}
	for _, t := range models {
		out.Printf("deploy: systemone smoke model=%s\n", t.Model)
	}
	return nil
}

func startStack(ctx context.Context, opts Options, p plan, cc compose.Client, out *progress.Printer) error {
	if !opts.SkipPull {
		if err := phase(ctx, opts, "compose.pull", cc.Pull); err != nil {
			return err
		}
	}
	return phase(ctx, opts, "compose.up", func(ctx context.Context) error {
		if err := cc.Up(ctx); err != nil {
			return err
		}
		if !p.services.Routers {
			// compose up leaves a profile's containers running once the profile is off.
			out.Println("deploy: remove the switchyard container if present")
			if err := cc.RemoveServices(ctx, stack.ServiceSwitchyard); err != nil {
				return err
			}
		}
		if !p.services.Observability {
			// compose up leaves a profile's containers running once the profile is off.
			out.Println("deploy: remove collector, Phoenix and HyperDX containers if present (their volumes stay)")
			if err := cc.RemoveServices(ctx, observabilityServices...); err != nil {
				return err
			}
		}
		reload := reloadServices(p.services)
		out.Printf("deploy: recreate %s (reload bind-mounted config)\n", strings.Join(reload, ", "))
		if opts.PruneFunc == nil {
			opts.PruneFunc = func(ctx context.Context, gatewayPath string) error {
				return overlay.PruneContainer(ctx, gatewayPath, overlay.ContainerConfig{Compose: cc, Out: opts.Out})
			}
		}
		return postComposeUpReload(ctx, opts, out, p.gatewayPath, cc.RecreateServices, reload...)
	})
}

func runSmoke(ctx context.Context, opts Options, p plan, out *progress.Printer) error {
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
		out.Printf("deploy: smoke sends the client key (%s)\n", secrets.ClientKeyName)
	}
	if p.checks.Providers {
		out.Println("deploy: provider chat smoke enabled (POLYPUS_SMOKE_PROVIDERS=0 to disable)")
	}
	run := opts.Smoke
	if run == nil {
		run = smoke.Run
	}
	return run(smokeCtx, smoke.Options{
		BaseURL:       opts.HealthURL,
		ChatModel:     opts.SmokeChatModel,
		WithRouter:    p.checks.Routers,
		WithSystemOne: p.checks.SystemOne,
		WithProviders: p.checks.Providers,
		GatewayConfig: p.gatewayPath,
		EnvDeploy:     opts.EnvDeployPath(),
		APIKey:        clientKey,
		Out:           opts.Out,
	})
}

func postComposeUpReload(
	ctx context.Context,
	opts Options,
	out *progress.Printer,
	gatewayPath string,
	recreate func(context.Context, ...string) error,
	services ...string,
) error {
	if err := runPostUpOverlayPrune(ctx, opts, out, gatewayPath); err != nil {
		return err
	}
	return recreate(ctx, services...)
}

func runPostUpOverlayPrune(ctx context.Context, opts Options, out *progress.Printer, gatewayPath string) error {
	const op = "deploy.overlayPrune"
	if opts.SkipOverlayPrune {
		out.Println("deploy: skip overlay prune (--skip-overlay-prune)")
		return nil
	}
	if opts.PruneFunc == nil {
		return derrors.New(derrors.CodeInvalid, op, "no prune function")
	}
	if err := opts.PruneFunc(ctx, gatewayPath); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "overlay prune")
	}
	return nil
}
