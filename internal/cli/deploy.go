package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/behaviorengineering/polypus/internal/deploy"
	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	deploysmoke "github.com/behaviorengineering/polypus/internal/deploy/smoke"
	"github.com/spf13/cobra"
)

const (
	deployJobTimeout = 2 * time.Minute
	healthURLHelp    = "gateway base URL (default: the environment's https://polypus-<identifier>.<tailnet>)"
	deployConfigHelp = "the environment's config.yaml (default: " + envconfig.ConfigEnv + ", then ~/.config/polypus/deploy/config.yaml)"
)

// deployTarget is the flags every deploy command shares: which environment it
// deploys, and which images it pins.
type deployTarget struct {
	config string
	images string
}

func (t *deployTarget) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.config, "config", "", deployConfigHelp)
	cmd.Flags().StringVar(&t.images, "images", "", "a pinned images file (default: the environment's images:, else this build's release images)")
}

func (t *deployTarget) options() (deploy.Options, error) {
	env, err := envconfig.Discover(t.config)
	if err != nil {
		return deploy.Options{}, err
	}
	return t.optionsFor(env)
}

// optionsFor builds the deploy options for an environment already discovered.
func (t *deployTarget) optionsFor(env envconfig.Environment) (deploy.Options, error) {
	stateDir, err := deploy.DefaultStateDir()
	if err != nil {
		return deploy.Options{}, err
	}
	opts := deploy.DefaultOptions(stateDir)
	opts.Environment = env
	opts.Name = env.Name()
	opts.HealthURL = env.GatewayURL()
	opts.Tailnet = env.Tailnet
	opts.GatewayConfig = env.Gateway
	opts.PinsFile = env.Images
	if t.images != "" {
		opts.PinsFile = t.images
	}
	opts.Version = buildinfo.Version
	return opts, nil
}

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "deploy",
		Short:   "Deploy Polypus with Docker Compose for an environment",
		GroupID: groupMutate,
		Long: `Deploy Polypus, Switchyard, the OTLP collector, Phoenix, HyperDX and Tailscale
with Docker Compose, for one environment: a config.yaml that names the environment
and points at its gateway YAML. polypus deploy init writes a starting one under
~/.config/polypus/deploy/.

Each environment is its own compose project, Tailscale host and keyring service,
all named polypus-<identifier>. Nothing publishes host ports: the gateway is reached
at https://polypus-<identifier>.<tailnet>, so this machine must be on that tailnet.
Secrets come from this shell, then that keyring service, then the optional SOPS
file ~/.config/polypus-<identifier>/secrets.enc.yaml; never from files in a repo.
Images default to this build's release; pass --images to pin others.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
	cmd.AddCommand(newDeployInitCmd())
	cmd.AddCommand(newDeployRunCmd())
	cmd.AddCommand(newDeploySmokeCmd())
	cmd.AddCommand(newDeploySmokeCollectorCmd())
	cmd.AddCommand(newDeployVerifyImagesCmd())
	cmd.AddCommand(newDeployExportEnvCmd())
	cmd.AddCommand(newDeployClientKeyCmd())
	cmd.AddCommand(newDeploySecretCmd())
	return cmd
}

func newDeployInitCmd() *cobra.Command {
	var force, dryRun bool
	cmd := &cobra.Command{
		Use:     "init",
		Short:   "Write a starting environment under ~/.config/polypus/deploy/",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			plan := envconfig.Init
			if dryRun {
				plan = envconfig.PlanInit
			}
			res, err := plan(force)
			if err != nil {
				return err
			}
			return writeInitResult(cmd, res, dryRun)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite config.yaml and gateway.yaml when they exist")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what init would write; write nothing")
	return cmd
}

func writeInitResult(cmd *cobra.Command, res envconfig.InitResult, dryRun bool) error {
	verb := func(wrote bool) string {
		switch {
		case wrote && dryRun:
			return "would write"
		case wrote:
			return "wrote"
		default:
			return "kept"
		}
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), `Environment
  config.yaml:   %s %s
  gateway.yaml:  %s %s

Next
  Set tailnet (and identifier) in config.yaml, then: polypus deploy run --dry-run
`, verb(res.WroteConfig), res.ConfigPath, verb(res.WroteGateway), res.GatewayPath)
	return err
}

// deployRunFlags are deploy run's own flags. apply copies them onto the
// environment's options in one place, so a flag cannot be bound and then dropped.
type deployRunFlags struct {
	dryRun, skipPull, skipSmoke, skipOverlayPrune bool
	healthURL, smokeChatModel                     string
	healthTimeout, smokeTimeout                   time.Duration
}

func (f deployRunFlags) apply(opts *deploy.Options) {
	opts.DryRun = f.dryRun
	opts.SkipPull = f.skipPull
	if url := strings.TrimRight(f.healthURL, "/"); url != "" {
		// An explicit URL is reached however the operator chose; skip the tailnet check.
		opts.HealthURL = url
		opts.Tailnet = ""
	}
	opts.HealthTimeout = f.healthTimeout
	opts.SkipSmoke = f.skipSmoke
	opts.SkipOverlayPrune = f.skipOverlayPrune
	opts.SmokeTimeout = f.smokeTimeout
	opts.SmokeChatModel = f.smokeChatModel
}

func newDeployRunCmd() *cobra.Command {
	var t deployTarget
	var f deployRunFlags
	var from secretsFrom
	cmd := &cobra.Command{
		Use:     "run",
		Short:   "Deploy the stack, then check health and smoke",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		PreRunE: envBoolDefaults(
			envBoolFlag{name: "skip-smoke", env: "POLYPUS_DEPLOY_SKIP_SMOKE", dst: &f.skipSmoke},
			envBoolFlag{name: "skip-overlay-prune", env: "POLYPUS_DEPLOY_SKIP_OVERLAY_PRUNE", dst: &f.skipOverlayPrune},
		),
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := t.options()
			if err != nil {
				return err
			}
			f.apply(&opts)
			opts.Out = cmd.OutOrStdout()
			if opts.Secrets, err = from.resolver(cmd); err != nil {
				return err
			}
			return secretsConflictExit(cmd, withTracing(cmd, func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, opts.Budget())
				defer cancel()
				return deploy.Run(ctx, opts)
			}))
		},
	}
	t.bind(cmd)
	from.bind(cmd)
	fl := cmd.Flags()
	fl.BoolVar(&f.dryRun, "dry-run", false, "resolve and print the plan; write nothing and change no containers")
	fl.BoolVar(&f.skipPull, "skip-pull", false, "skip docker compose pull")
	fl.StringVar(&f.healthURL, "health-url", "", healthURLHelp)
	fl.DurationVar(&f.healthTimeout, "health-timeout", 120*time.Second, "health poll timeout")
	fl.BoolVar(&f.skipSmoke, "skip-smoke", false, "skip the inference smoke after health (env POLYPUS_DEPLOY_SKIP_SMOKE=1)")
	fl.BoolVar(&f.skipOverlayPrune, "skip-overlay-prune", false, "skip the models allow overlay prune before the gateway is recreated (env POLYPUS_DEPLOY_SKIP_OVERLAY_PRUNE=1)")
	bindSmokeModelFlags(cmd, &f.smokeTimeout, &f.smokeChatModel)
	return cmd
}

func bindSmokeModelFlags(cmd *cobra.Command, timeout *time.Duration, chat *string) {
	f := cmd.Flags()
	f.DurationVar(timeout, "smoke-timeout", deploysmoke.DefaultSmokeTimeout, "inference smoke timeout")
	f.StringVar(chat, "smoke-chat-model", "", "direct chat smoke model (default: the first chat model in the gateway YAML)")
}

// deploySmokeFlags are deploy smoke's flags. The With* values hold the env
// default when the flag is not passed (see PreRunE).
type deploySmokeFlags struct {
	healthURL, chatModel, envDeploy          string
	withRouter, withSystemOne, withProviders bool
	timeout                                  time.Duration
}

func newDeploySmokeCmd() *cobra.Command {
	var t deployTarget
	var f deploySmokeFlags
	var from secretsFrom
	cmd := &cobra.Command{
		Use:   "smoke",
		Short: "Chat inference smoke against a deployed gateway",
		Long: `Chat inference smoke against a deployed gateway.

With an environment, smoke repeats the checks polypus deploy run makes: one chat per
backend in the gateway YAML whose secrets are set, each chat router, and SystemOne
when the YAML enables it. A --with-* flag you pass, or --smoke-chat-model, wins over
that. Without an environment, smoke sends one chat to --smoke-chat-model.`,
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		PreRunE: envBoolDefaults(
			envBoolFlag{name: "with-router", env: "POLYPUS_SWITCHYARD", dst: &f.withRouter},
			envBoolFlag{name: "with-systemone", env: "POLYPUS_SMOKE_SYSTEMONE", dst: &f.withSystemOne},
			envBoolFlag{name: "with-providers", env: "POLYPUS_SMOKE_PROVIDERS", dst: &f.withProviders},
		),
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, err := deploySmokeRequest(cmd, &t, f, &from)
			if err != nil {
				return err
			}
			o, err := deploy.ResolveSmoke(req)
			if err != nil {
				return secretsConflictExit(cmd, err)
			}
			return withTracing(cmd, func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, f.timeout)
				defer cancel()
				if err := deploysmoke.Run(ctx, o); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "smoke: ok")
				return err
			})
		},
	}
	t.bind(cmd)
	from.bind(cmd)
	fl := cmd.Flags()
	fl.StringVar(&f.healthURL, "health-url", "", healthURLHelp)
	// POLYPUS_SWITCHYARD is read with the same parser serve uses (see PreRunE).
	// Unset means off here: a router smoke needs an environment's gateway YAML, and
	// with one the deploy run checks decide instead.
	fl.BoolVar(&f.withRouter, "with-router", false, "also smoke each chat router in the gateway YAML, reporting the model that answered (default with an environment: as deploy run)")
	fl.BoolVar(&f.withSystemOne, "with-systemone", false, "also smoke /v1/systemone for the gateway YAML's SystemOne backends (default with an environment: as deploy run)")
	fl.BoolVar(&f.withProviders, "with-providers", false, "smoke chat per backend in the gateway YAML whose secrets are set, in place of --smoke-chat-model (default with an environment: as deploy run)")
	fl.StringVar(&f.envDeploy, "env-deploy", "", "dotenv with secret names for --with-providers (default: .env.deploy in the environment's state folder)")
	bindSmokeModelFlags(cmd, &f.timeout, &f.chatModel)
	return cmd
}

// deploySmokeRequest maps deploy smoke's flags onto a deploy.SmokeRequest. With an
// environment, a --with-* flag the operator did not pass is left unset, so the
// deploy run checks decide it; without one, it keeps its env default.
func deploySmokeRequest(cmd *cobra.Command, t *deployTarget, f deploySmokeFlags, from *secretsFrom) (deploy.SmokeRequest, error) {
	req := deploy.SmokeRequest{
		BaseURL:   f.healthURL,
		EnvDeploy: f.envDeploy,
		ChatModel: f.chatModel,
		Out:       cmd.OutOrStdout(),
	}
	env, err := envconfig.Discover(t.config)
	found := err == nil
	switch {
	case errors.Is(err, envconfig.ErrNotFound):
		req.NoEnvironment = err
	case err != nil:
		return req, err
	}
	flag := func(name string, v bool) *bool {
		if found && !cmd.Flags().Changed(name) {
			return nil
		}
		return &v
	}
	req.WithRouter = flag("with-router", f.withRouter)
	req.WithSystemOne = flag("with-systemone", f.withSystemOne)
	req.WithProviders = flag("with-providers", f.withProviders)
	if !found {
		return req, nil
	}
	opts, err := t.optionsFor(env)
	if err != nil {
		return req, err
	}
	if opts.Secrets, err = from.resolver(cmd); err != nil {
		return req, err
	}
	req.Deploy = &opts
	return req, nil
}

func newDeploySmokeCollectorCmd() *cobra.Command {
	var o deploysmoke.CollectorOptions
	var timeout time.Duration
	var withHyperDX bool
	var cfgPath string
	cmd := &cobra.Command{
		Use:     "smoke-collector",
		Short:   "OTLP fan-out smoke (otelcol to Phoenix and HyperDX)",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, found, err := discoverOptional(cfgPath)
			if err != nil {
				return err
			}
			gatewayURL := ""
			if found {
				gatewayURL = env.GatewayURL()
				if o.HyperDXContainer == "" {
					// compose names it <project>-<service>-1.
					o.HyperDXContainer = env.Name() + "-hyperdx-1"
				}
			}
			opts := deploysmoke.ResolveCollectorOptions(o, gatewayURL)
			if withHyperDX {
				opts.SkipHyperDXVerify = false
			}
			return withTracing(cmd, func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				if err := deploysmoke.RunCollector(ctx, opts); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "smoke-collector: ok")
				return err
			})
		},
	}
	f := cmd.Flags()
	f.DurationVar(&timeout, "timeout", deploysmoke.DefaultCollectorSmokeTimeout, "collector smoke timeout")
	f.StringVar(&o.OTLPEndpoint, "otlp", "", "OTLP endpoint (http://host:4318 or grpc host:4317)")
	f.StringVar(&o.PhoenixBaseURL, "phoenix-url", "", "Phoenix base URL")
	f.StringVar(&o.PhoenixProject, "phoenix-project", "", "Phoenix project name")
	f.StringVar(&o.HyperDXBaseURL, "hyperdx-url", "", "HyperDX base URL")
	f.StringVar(&o.HyperDXContainer, "hyperdx-container", "", "docker container for the ClickHouse fallback (default: the environment's, polypus-<identifier>-hyperdx-1)")
	f.StringVar(&cfgPath, "config", "", deployConfigHelp+"; names its HyperDX container")
	f.BoolVar(&withHyperDX, "with-hyperdx", false, "verify HyperDX (skipped by default when POLYPUS_BASE_URL is a remote host)")
	return cmd
}

// discoverOptional loads the environment when one is found. With none, found is
// false and the caller keeps its own defaults.
func discoverOptional(cfgPath string) (envconfig.Environment, bool, error) {
	env, err := envconfig.Discover(cfgPath)
	if errors.Is(err, envconfig.ErrNotFound) {
		return envconfig.Environment{}, false, nil
	}
	if err != nil {
		return envconfig.Environment{}, false, err
	}
	return env, true, nil
}

func newDeployVerifyImagesCmd() *cobra.Command {
	var t deployTarget
	cmd := &cobra.Command{
		Use:     "verify-images",
		Short:   "Check image pins, GHCR manifests and the release's publish-complete",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := t.options()
			if err != nil {
				return err
			}
			return withTracing(cmd, func(ctx context.Context) error {
				ctx, cancel := context.WithTimeout(ctx, deployJobTimeout)
				defer cancel()
				if err := deploy.VerifyImages(ctx, opts); err != nil {
					return err
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "verify-images: ok")
				return err
			})
		},
	}
	t.bind(cmd)
	return cmd
}

func newDeployExportEnvCmd() *cobra.Command {
	var t deployTarget
	var app, out, secretCSV string
	var dryRun bool
	var from secretsFrom
	cmd := &cobra.Command{
		Use:     "export-env",
		Short:   "Write keyring secrets named by the gateway YAML to .env.deploy and .env.gateway",
		GroupID: groupSetup,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := t.options()
			if err != nil {
				return err
			}
			if out == "" {
				out = opts.EnvDeployPath()
			}
			if csv := strings.TrimSpace(secretCSV); csv != "" {
				opts.SecretNames = splitCSV(csv)
			}
			names, err := deploy.ExportSecretNames(opts)
			if err != nil {
				return err
			}
			if dryRun {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "export-env: would write %s\n  secrets: %s\n", out, strings.Join(names, ","))
				return err
			}
			if app == "" {
				app = opts.Name
			}
			r, err := from.resolver(cmd)
			if err != nil {
				return err
			}
			if err := secrets.Export(r, app, names, out); err != nil {
				return secretsConflictExit(cmd, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "export-env: wrote %s and %s beside it\n", out, secrets.GatewayEnvFileName)
			return err
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&app, "app", "", "keyring service (default: polypus-<identifier>)")
	f.StringVar(&out, "out", "", "dotenv path (default: .env.deploy in the environment's state folder)")
	f.StringVar(&secretCSV, "secrets", "", "comma-separated env names (default: from the gateway YAML)")
	f.BoolVar(&dryRun, "dry-run", false, "print the path and secret names; write nothing")
	from.bind(cmd)
	return cmd
}
