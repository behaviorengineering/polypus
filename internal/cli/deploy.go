package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/behaviorengineering/polypus/internal/deploy"
	"github.com/behaviorengineering/polypus/internal/deploy/images"
	"github.com/behaviorengineering/polypus/internal/deploy/paths"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/deploy/smoke"
	"github.com/spf13/cobra"
)

const (
	deployJobTimeout    = 2 * time.Minute
	deployEnsureBudget  = 25 * time.Minute
	deployDefaultHealth = "http://127.0.0.1:1320"
)

// deployTarget is the flags every deploy command shares: where the Polypus checkout
// is, which gateway YAML the environment deploys, and which images it pins.
type deployTarget struct {
	root          string
	gatewayConfig string
	images        string
}

func (t *deployTarget) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.root, "root", "", "Polypus checkout holding docker-compose.deploy.yml (default: found above the working directory)")
	cmd.Flags().StringVar(&t.gatewayConfig, "gateway-config", "", "the environment's gateway YAML (required)")
	cmd.Flags().StringVar(&t.images, "images", "", "the environment's pinned images file (default: this build's release images)")
}

func (t *deployTarget) options() (deploy.Options, error) {
	start := t.root
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return deploy.Options{}, fmt.Errorf("deploy: working directory: %w", err)
		}
		start = wd
	}
	root, err := paths.FindRepoRoot(start)
	if err != nil {
		return deploy.Options{}, err
	}
	opts := deploy.DefaultOptions(root)
	opts.GatewayConfig = t.gatewayConfig
	opts.PinsFile = t.images
	opts.Version = buildinfo.Version
	return opts, nil
}

func newDeployCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy Polypus with Docker Compose for an environment",
		Long: `Deploy Polypus, Switchyard, the OTLP collector, Phoenix, HyperDX and Tailscale
with Docker Compose, from an environment's gateway YAML.

Secrets come from the OS keyring (service "polypus"), never from files in a repo.
Images default to this build's release; pass --images to pin others.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := cmd.Help(); err != nil {
				return err
			}
			return exitStatus(2)
		},
	}
	cmd.AddCommand(newDeployRunCmd())
	cmd.AddCommand(newDeploySmokeCmd())
	cmd.AddCommand(newDeploySmokeCollectorCmd())
	cmd.AddCommand(newDeployVerifyImagesCmd())
	cmd.AddCommand(newDeployExportEnvCmd())
	return cmd
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
			opts.HealthURL = strings.TrimRight(o.HealthURL, "/")
			opts.HealthTimeout = o.HealthTimeout
			opts.SkipSmoke = o.SkipSmoke
			opts.SkipOverlayPrune = o.SkipOverlayPrune
			opts.SmokeTimeout = o.SmokeTimeout
			opts.SmokeChatModel = o.SmokeChatModel
			opts.SmokeRouterModel = o.SmokeRouterModel
			opts.SmokeCapable = o.SmokeCapable
			opts.SmokeEfficient = o.SmokeEfficient
			ctx, cancel := context.WithTimeout(cmd.Context(), opts.HealthTimeout+opts.SmokeTimeout+deployEnsureBudget)
			defer cancel()
			return deploy.Run(ctx, opts)
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.BoolVar(&o.DryRun, "dry-run", false, "resolve and print the plan; write nothing and change no containers")
	f.BoolVar(&o.SkipPull, "skip-pull", false, "skip docker compose pull")
	f.StringVar(&o.HealthURL, "health-url", deployDefaultHealth, "gateway base URL")
	f.DurationVar(&o.HealthTimeout, "health-timeout", 120*time.Second, "health poll timeout")
	f.BoolVar(&o.SkipSmoke, "skip-smoke", os.Getenv("POLYPUS_DEPLOY_SKIP_SMOKE") == "1", "skip the inference smoke after health (env POLYPUS_DEPLOY_SKIP_SMOKE=1)")
	f.BoolVar(&o.SkipOverlayPrune, "skip-overlay-prune", os.Getenv("POLYPUS_DEPLOY_SKIP_OVERLAY_PRUNE") == "1", "skip the models allow overlay prune before the gateway is recreated (env POLYPUS_DEPLOY_SKIP_OVERLAY_PRUNE=1)")
	bindSmokeModelFlags(cmd, &o.SmokeTimeout, &o.SmokeChatModel, &o.SmokeRouterModel, &o.SmokeCapable, &o.SmokeEfficient)
	return cmd
}

func bindSmokeModelFlags(cmd *cobra.Command, timeout *time.Duration, chat, router, capable, efficient *string) {
	f := cmd.Flags()
	f.DurationVar(timeout, "smoke-timeout", smoke.DefaultSmokeTimeout, "inference smoke timeout")
	f.StringVar(chat, "smoke-chat-model", "", "direct chat smoke model")
	f.StringVar(router, "smoke-router-model", "", "router smoke model")
	f.StringVar(capable, "smoke-router-capable", "", "expected capable leaf for router smoke")
	f.StringVar(efficient, "smoke-router-efficient", "", "expected efficient leaf for router smoke")
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
			if o.WithProviders || o.WithSystemOne {
				gw, err := paths.ResolveGatewayConfig(t.gatewayConfig)
				if err != nil {
					return err
				}
				o.GatewayConfig = gw
			}
			if o.WithProviders && strings.TrimSpace(envDeploy) == "" {
				opts, err := t.options()
				if err != nil {
					return err
				}
				envDeploy = opts.EnvDeploy
			}
			o.EnvDeploy = envDeploy
			o.BaseURL = strings.TrimRight(o.BaseURL, "/")
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := smoke.Run(ctx, o); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "smoke: ok")
			return err
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&o.BaseURL, "health-url", deployDefaultHealth, "gateway base URL")
	f.BoolVar(&o.WithRouter, "with-router", os.Getenv("POLYPUS_SWITCHYARD") == "1", "also smoke router/investigator with the selected-leaf check")
	f.BoolVar(&o.WithSystemOne, "with-systemone", os.Getenv("POLYPUS_SMOKE_SYSTEMONE") == "1", "also smoke /v1/systemone for the gateway YAML's SystemOne backends")
	f.BoolVar(&o.WithProviders, "with-providers", os.Getenv("POLYPUS_SMOKE_PROVIDERS") == "1", "smoke chat per backend in the gateway YAML (secret names from --env-deploy)")
	f.StringVar(&envDeploy, "env-deploy", "", "dotenv with secret names for --with-providers (default: .env.deploy in the checkout)")
	bindSmokeModelFlags(cmd, &timeout, &o.ChatModel, &o.RouterModel, &o.Capable, &o.Efficient)
	return cmd
}

func newDeploySmokeCollectorCmd() *cobra.Command {
	var o smoke.CollectorOptions
	var timeout time.Duration
	var withHyperDX bool
	cmd := &cobra.Command{
		Use:     "smoke-collector",
		Short:   "OTLP fan-out smoke (otelcol to Phoenix and HyperDX)",
		GroupID: groupInspect,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts := smoke.ResolveCollectorOptions(o)
			if withHyperDX {
				opts.SkipHyperDXVerify = false
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			if err := smoke.RunCollector(ctx, opts); err != nil {
				return err
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "smoke-collector: ok")
			return err
		},
	}
	f := cmd.Flags()
	f.DurationVar(&timeout, "timeout", smoke.DefaultCollectorSmokeTimeout, "collector smoke timeout")
	f.StringVar(&o.OTLPEndpoint, "otlp", "", "OTLP endpoint (http://host:4318 or grpc host:4317)")
	f.StringVar(&o.PhoenixBaseURL, "phoenix-url", "", "Phoenix base URL")
	f.StringVar(&o.PhoenixProject, "phoenix-project", "", "Phoenix project name")
	f.StringVar(&o.HyperDXBaseURL, "hyperdx-url", "", "HyperDX base URL")
	f.StringVar(&o.HyperDXContainer, "hyperdx-container", "", "docker container for the ClickHouse fallback")
	f.BoolVar(&withHyperDX, "with-hyperdx", false, "verify HyperDX (skipped by default when POLYPUS_BASE_URL is a remote host)")
	return cmd
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
			path, err := verifyImagesFile(opts)
			if err != nil {
				return err
			}
			if path != opts.PinsFile {
				defer func() { _ = os.Remove(path) }()
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), deployJobTimeout)
			defer cancel()
			if err := images.EnsurePresent(ctx, path, images.EnsureOptions{Wait: false}); err != nil {
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
func verifyImagesFile(opts deploy.Options) (string, error) {
	if strings.TrimSpace(opts.PinsFile) != "" {
		return opts.PinsFile, images.Verify(opts.PinsFile)
	}
	pins, err := images.VersionPins(opts.RepoPins, opts.Version)
	if err != nil {
		return "", err
	}
	if err := images.VerifyPins(pins, opts.RepoPins); err != nil {
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
				out = opts.EnvDeploy
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
			if err := secrets.Export(app, names, out); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "export-env: wrote %s\n", out)
			return err
		},
	}
	t.bind(cmd)
	f := cmd.Flags()
	f.StringVar(&app, "app", "polypus", "keyring service")
	f.StringVar(&out, "out", "", "dotenv path (default: .env.deploy in the checkout)")
	f.StringVar(&secretCSV, "secrets", "", "comma-separated env names (default: from the gateway YAML)")
	f.BoolVar(&dryRun, "dry-run", false, "print the path and secret names; write nothing")
	return cmd
}
