package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/behaviorengineering/polypus/internal/deploy"
	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/images"
	"github.com/behaviorengineering/polypus/internal/deploy/paths"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
	"github.com/behaviorengineering/polypus/internal/deploy/stack"
	"github.com/spf13/cobra"
)

const (
	deployJobTimeout   = 2 * time.Minute
	deployEnsureBudget = 25 * time.Minute
	healthURLHelp      = "gateway base URL (default: the environment's https://polypus-<identifier>.<tailnet>)"
)

// deployTarget is the flags every deploy command shares: which environment it
// deploys, and which images it pins.
type deployTarget struct {
	config string
	images string
}

func (t *deployTarget) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.config, "config", "", "the environment's config.yaml (default: "+envconfig.ConfigEnv+", then ~/.config/polypus/deploy/config.yaml)")
	cmd.Flags().StringVar(&t.images, "images", "", "a pinned images file (default: the environment's images:, else this build's release images)")
}

func (t *deployTarget) options() (deploy.Options, error) {
	stateDir, err := deploy.DefaultStateDir()
	if err != nil {
		return deploy.Options{}, err
	}
	env, err := envconfig.Discover(t.config)
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
		Use:   "deploy",
		Short: "Deploy Polypus with Docker Compose for an environment",
		Long: `Deploy Polypus, Switchyard, the OTLP collector, Phoenix, HyperDX and Tailscale
with Docker Compose, for one environment: a config.yaml that names the environment
and points at its gateway YAML. polypus deploy init writes a starting one under
~/.config/polypus/deploy/.

Each environment is its own compose project, Tailscale host and keyring service,
all named polypus-<identifier>. Nothing publishes host ports: the gateway is reached
at https://polypus-<identifier>.<tailnet>, so this machine must be on that tailnet. Secrets come from that keyring service, never from
files in a repo.
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

func newDeployRunCmd() *cobra.Command {
	var t deployTarget
	var o deploy.Options
	cmd := &cobra.Command{
		Use:     "run",
		Short:   "Deploy the stack, then check health and smoke",
		GroupID: groupMutate,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := t.options()
			if err != nil {
				return err
			}
			opts.SkipPull = o.SkipPull
			opts.DryRun = o.DryRun
			if url := strings.TrimRight(o.HealthURL, "/"); url != "" {
				// An explicit URL is reached however the operator chose; skip the tailnet check.
				opts.HealthURL = url
				opts.Tailnet = ""
			}
			opts.HealthTimeout = o.HealthTimeout
			opts.SkipSmoke = o.SkipSmoke
			opts.SkipOverlayPrune = o.SkipOverlayPrune
			opts.SmokeTimeout = o.SmokeTimeout
			opts.SmokeChatModel = o.SmokeChatModel
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.HealthTimeout+opts.SmokeTimeout+deployEnsureBudget)
			defer cancel()
			return deploy.Run(ctx, opts)
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.DryRun, "dry-run", false, "resolve and print the plan; write nothing and change no containers")
	f.BoolVar(&o.SkipPull, "skip-pull", false, "skip docker compose pull")
	f.StringVar(&o.HealthURL, "health-url", "", healthURLHelp)
	f.DurationVar(&o.HealthTimeout, "health-timeout", 120*time.Second, "health poll timeout")
	f.BoolVar(&o.SkipSmoke, "skip-smoke", os.Getenv("POLYPUS_DEPLOY_SKIP_SMOKE") == "1", "skip the inference smoke after health (env POLYPUS_DEPLOY_SKIP_SMOKE=1)")
	f.BoolVar(&o.SkipOverlayPrune, "skip-overlay-prune", os.Getenv("POLYPUS_DEPLOY_SKIP_OVERLAY_PRUNE") == "1", "skip the models allow overlay prune before the gateway is recreated (env POLYPUS_DEPLOY_SKIP_OVERLAY_PRUNE=1)")
	bindSmokeModelFlags(cmd, &o.SmokeTimeout, &o.SmokeChatModel)
	return cmd
}

func bindSmokeModelFlags(cmd *cobra.Command, timeout *time.Duration, chat *string) {
	f := cmd.Flags()
	f.DurationVar(timeout, "smoke-timeout", smoke.DefaultSmokeTimeout, "inference smoke timeout")
	f.StringVar(chat, "smoke-chat-model", "", "direct chat smoke model")
}

func newDeploySmokeCmd() *cobra.Command {
	var t deployTarget
	var o smoke.Options
	var timeout time.Duration
	var envDeploy string
	cmd := &cobra.Command{
		Use:     "smoke",
		Short:   "Chat inference smoke against a deployed gateway",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, found, err := discoverOptional(t.config)
			if err != nil {
				return err
			}
			if found {
				// Env POLYPUS_API_KEY wins, then the environment's keyring service.
				if o.APIKey, err = secrets.Lookup(nil, env.Name(), secrets.ClientKeyName); err != nil {
					return err
				}
			}
			if o.WithProviders || o.WithSystemOne || o.WithRouter {
				opts, err := t.options()
				if err != nil {
					return err
				}
				if o.GatewayConfig, err = paths.ResolveGatewayConfig(opts.GatewayConfig); err != nil {
					return err
				}
				if strings.TrimSpace(envDeploy) == "" {
					envDeploy = opts.EnvDeployPath()
				}
			}
			o.EnvDeploy = envDeploy
			if o.BaseURL == "" {
				opts, err := t.options()
				if err != nil {
					return err
				}
				o.BaseURL = opts.HealthURL
			}
			o.BaseURL = strings.TrimRight(o.BaseURL, "/")
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := smoke.Run(ctx, o); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "smoke: ok")
			return err
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&o.BaseURL, "health-url", "", healthURLHelp)
	f.BoolVar(&o.WithRouter, "with-router", os.Getenv("POLYPUS_SWITCHYARD") == "1", "also smoke each chat router in the gateway YAML, reporting the model that answered")
	f.BoolVar(&o.WithSystemOne, "with-systemone", os.Getenv("POLYPUS_SMOKE_SYSTEMONE") == "1", "also smoke /v1/systemone for the gateway YAML's SystemOne backends")
	f.BoolVar(&o.WithProviders, "with-providers", os.Getenv("POLYPUS_SMOKE_PROVIDERS") == "1", "smoke chat per backend in the gateway YAML (secret names from --env-deploy)")
	f.StringVar(&envDeploy, "env-deploy", "", "dotenv with secret names for --with-providers (default: .env.deploy in the environment's state folder)")
	bindSmokeModelFlags(cmd, &timeout, &o.ChatModel)
	return cmd
}

func newDeploySmokeCollectorCmd() *cobra.Command {
	var o smoke.CollectorOptions
	var timeout time.Duration
	var withHyperDX bool
	var config string
	cmd := &cobra.Command{
		Use:     "smoke-collector",
		Short:   "OTLP fan-out smoke (otelcol to Phoenix and HyperDX)",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, found, err := discoverOptional(config)
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
			opts := smoke.ResolveCollectorOptions(o, gatewayURL)
			if withHyperDX {
				opts.SkipHyperDXVerify = false
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := smoke.RunCollector(ctx, opts); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "smoke-collector: ok")
			return err
		},
	}
	f := cmd.Flags()
	f.DurationVar(&timeout, "timeout", smoke.DefaultCollectorSmokeTimeout, "collector smoke timeout")
	f.StringVar(&o.OTLPEndpoint, "otlp", "", "OTLP endpoint (http://host:4318 or grpc host:4317)")
	f.StringVar(&o.PhoenixBaseURL, "phoenix-url", "", "Phoenix base URL")
	f.StringVar(&o.PhoenixProject, "phoenix-project", "", "Phoenix project name")
	f.StringVar(&o.HyperDXBaseURL, "hyperdx-url", "", "HyperDX base URL")
	f.StringVar(&o.HyperDXContainer, "hyperdx-container", "", "docker container for the ClickHouse fallback (default: the environment's, polypus-<identifier>-hyperdx-1)")
	f.StringVar(&config, "config", "", "the environment's config.yaml, to name its HyperDX container (default: "+envconfig.ConfigEnv+", then ~/.config/polypus/deploy/config.yaml)")
	f.BoolVar(&withHyperDX, "with-hyperdx", false, "verify HyperDX (skipped by default when POLYPUS_BASE_URL is a remote host)")
	return cmd
}

// discoverOptional loads the environment when one is found. With none, found is
// false and the caller keeps its own defaults.
func discoverOptional(config string) (envconfig.Environment, bool, error) {
	env, err := envconfig.Discover(config)
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
			svc, err := deploy.Services(opts)
			if err != nil {
				return err
			}
			path, err := verifyImagesFile(opts, svc)
			if err != nil {
				return err
			}
			if path != opts.PinsFile {
				defer func() { _ = os.Remove(path) }()
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), deployJobTimeout)
			defer cancel()
			if err := images.EnsurePresent(ctx, path, images.EnsureOptions{Wait: false, Services: svc}); err != nil {
				return err
			}
			ver, err := images.PolypusVersionFromEnv(path)
			if err != nil {
				return err
			}
			if err := images.VerifyPublishComplete(ctx, ver, images.DefaultWaitOptions()); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "verify-images: ok")
			return err
		},
	}
	t.bind(cmd)
	return cmd
}

// verifyImagesFile returns the pins file to verify: the environment's own, or this
// build's release pins written to a temporary file.
func verifyImagesFile(opts deploy.Options, svc images.Services) (string, error) {
	if strings.TrimSpace(opts.PinsFile) != "" {
		return opts.PinsFile, images.Verify(opts.PinsFile, svc)
	}
	pins, err := images.VersionPins(stack.ImagePins(), opts.Version)
	if err != nil {
		return "", err
	}
	if err := images.VerifyPins(pins, "the embedded images.env", svc); err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "polypus-images-*.env")
	if err != nil {
		return "", fmt.Errorf("verify-images: temp pins: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("verify-images: temp pins: %w", err)
	}
	return name, images.WritePins(name, pins)
}

func newDeployExportEnvCmd() *cobra.Command {
	var t deployTarget
	var app, out, secretCSV string
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "export-env",
		Short:   "Write keyring secrets named by the gateway YAML to .env.deploy",
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
			if err := secrets.Export(app, names, out); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "export-env: wrote %s\n", out)
			return err
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&app, "app", "", "keyring service (default: polypus-<identifier>)")
	f.StringVar(&out, "out", "", "dotenv path (default: .env.deploy in the environment's state folder)")
	f.StringVar(&secretCSV, "secrets", "", "comma-separated env names (default: from the gateway YAML)")
	f.BoolVar(&dryRun, "dry-run", false, "print the path and secret names; write nothing")
	return cmd
}
