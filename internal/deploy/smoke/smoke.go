// Package smoke runs a deploy's post-health checks against a running gateway: one
// chat per backend whose secrets are set, each chat router, and SystemOne, plus the
// OTLP collector fan-out smoke.
package smoke

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	internalsmoke "github.com/behaviorengineering/polypus/internal/smoke"
)

const defaultChatModel = internalsmoke.DefaultChatModel

// Options configures post-deploy smoke against a running gateway.
type Options struct {
	// BaseURL is the gateway; empty reads POLYPUS_BASE_URL. One of them is required.
	BaseURL   string
	ChatModel string
	// WithRouter smokes every chat router in GatewayConfig.
	WithRouter    bool
	WithSystemOne bool
	// SystemOneRequested is set when the operator passed --with-systemone, so a run
	// that skipped every SystemOne backend says so instead of passing quietly.
	SystemOneRequested bool
	SystemOneModel     string
	// WithProviders runs chat probes per backend from GatewayConfig (secrets gated via EnvDeploy).
	WithProviders bool
	GatewayConfig string
	EnvDeploy     string
	// APIKey is the gateway's client key, sent with every probe when set.
	APIKey string
	// Out receives one line per probe; nil writes to os.Stdout.
	Out io.Writer
}

// endpoint is the gateway the probes call, the key they send and where their
// results are printed.
type endpoint struct {
	url string
	key string
	out *progress.Printer
}

// Run executes direct chat smoke and optional router smoke with selected-leaf checks.
func Run(ctx context.Context, opts Options) error {
	const op = "smoke.Run"
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, op, "missing deadline")
	}
	opts = opts.withDefaults()
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return derrors.New(derrors.CodeInvalid, op, "base URL required (--health-url or POLYPUS_BASE_URL)")
	}
	ep := endpoint{url: base, key: strings.TrimSpace(opts.APIKey), out: progress.New(opts.Out)}
	if err := run(ctx, ep, opts); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "smoke "+base)
	}
	return ep.out.Err()
}

func run(ctx context.Context, ep endpoint, opts Options) error {
	if opts.WithProviders {
		if err := runProviderSmokes(ctx, ep, opts); err != nil {
			return err
		}
	} else if err := runChat(ctx, ep, opts.ChatModel, false, nil); err != nil {
		return err
	}
	if opts.WithRouter {
		if err := runRouterSmokes(ctx, ep, opts.GatewayConfig); err != nil {
			return err
		}
	}
	if !opts.WithSystemOne {
		return nil
	}
	return runSystemOneProbes(ctx, ep, opts)
}

func (o Options) withDefaults() Options {
	out := o
	if strings.TrimSpace(out.BaseURL) == "" {
		// No localhost default: a smoke with no gateway named fails instead of
		// probing whatever answers on this machine.
		out.BaseURL = strings.TrimSpace(os.Getenv("POLYPUS_BASE_URL"))
	}
	if strings.TrimSpace(out.ChatModel) == "" {
		out.ChatModel = config.EnvOr("POLYPUS_CHAT_SMOKE_MODEL", defaultChatModel)
	}
	if strings.TrimSpace(out.SystemOneModel) == "" {
		out.SystemOneModel = config.EnvOr("POLYPUS_SYSTEMONE_SMOKE_MODEL", internalsmoke.DefaultSystemOneModel)
	}
	return out
}

func runChat(ctx context.Context, ep endpoint, model string, requireSelected bool, allowed []string) error {
	return runProbe(ctx, ep, model, internalsmoke.Options{
		Channels:              []string{internalsmoke.ChannelChat},
		ChatModel:             model,
		MaxTokens:             64,
		Temperature:           0.2,
		RequireSelectedModel:  requireSelected,
		AllowedSelectedModels: allowed,
	})
}

func runSystemOne(ctx context.Context, ep endpoint, model string) error {
	return runProbe(ctx, ep, model, internalsmoke.Options{
		Channels:       []string{internalsmoke.ChannelSystemOne},
		SystemOneModel: model,
	})
}

// runProbe runs one smoke probe for model against ep and prints each result. A
// failed result fails the probe.
func runProbe(ctx context.Context, ep endpoint, model string, popts internalsmoke.Options) error {
	popts.BaseURL = ep.url
	popts.APIKey = ep.key
	results, err := internalsmoke.Run(ctx, popts)
	for _, r := range results {
		ep.out.Printf("smoke: channel=%s probe=%s model=%s selected=%s status=%s ms=%d detail=%s\n",
			r.Channel, r.Probe, r.Model, r.SelectedModel, r.Status, r.Ms, r.Detail)
		if r.Status == internalsmoke.StatusFail {
			return fmt.Errorf("smoke %s probe %s: %s", model, r.Probe, r.Detail)
		}
	}
	if err != nil {
		return fmt.Errorf("smoke %s: %w", model, err)
	}
	return nil
}

// runRouterSmokes sends one chat through each chat router in the gateway YAML and
// reports the model that answered. A composed router must answer with one of its
// leaves; a passthrough router reports the upstream model only.
func runRouterSmokes(ctx context.Context, ep endpoint, gatewayConfig string) error {
	if strings.TrimSpace(gatewayConfig) == "" {
		return fmt.Errorf("smoke: gateway config required to find its routers")
	}
	targets, err := gatewayconfig.RouterSmokeTargets(gatewayConfig)
	if err != nil {
		return fmt.Errorf("smoke: routers: %w", err)
	}
	if len(targets) == 0 {
		return fmt.Errorf("smoke: no chat routers in %s", gatewayConfig)
	}
	for _, t := range targets {
		var allowed []string
		if t.Composed {
			allowed = t.Leaves
		}
		if err := runChat(ctx, ep, t.Model, true, allowed); err != nil {
			return err
		}
	}
	return nil
}

func runSystemOneProbes(ctx context.Context, ep endpoint, opts Options) error {
	if strings.TrimSpace(opts.GatewayConfig) == "" {
		return runSystemOne(ctx, ep, opts.SystemOneModel)
	}
	targets, err := gatewayconfig.SystemOneSmokeTargets(opts.GatewayConfig)
	if err != nil {
		return fmt.Errorf("smoke: systemone targets: %w", err)
	}
	if err := gatewayconfig.ValidateChatSmokeTargets(targets); err != nil {
		return err
	}
	if len(targets) == 0 {
		return runSystemOne(ctx, ep, opts.SystemOneModel)
	}
	present, err := secrets.NonEmptyDotenvKeys(opts.EnvDeploy)
	if err != nil {
		return fmt.Errorf("smoke: dotenv: %w", err)
	}
	// A backend without its secrets is skipped, as the provider probes do: the
	// deploy has no key for it, so a probe could only fail.
	var ran int
	for _, t := range targets {
		if !gatewayconfig.SecretsReady(t, present) {
			miss := gatewayconfig.MissingSecrets(t, present)
			ep.out.Printf("smoke: skip systemone backend=%s model=%s (missing %s)\n", t.BackendID, t.Model, strings.Join(miss, ","))
			continue
		}
		if err := runSystemOne(ctx, ep, t.Model); err != nil {
			return err
		}
		ran++
	}
	if ran == 0 && opts.SystemOneRequested {
		ep.out.Printf("smoke: systemone not run: --with-systemone was set but every SystemOne backend is missing a secret in %s\n", opts.EnvDeploy)
	}
	return nil
}

func runProviderSmokes(ctx context.Context, ep endpoint, opts Options) error {
	gw := strings.TrimSpace(opts.GatewayConfig)
	if gw == "" {
		return fmt.Errorf("smoke: gateway config required with --with-providers")
	}
	targets, err := gatewayconfig.ChatSmokeTargets(gw)
	if err != nil {
		return fmt.Errorf("smoke: provider targets: %w", err)
	}
	if err := gatewayconfig.ValidateChatSmokeTargets(targets); err != nil {
		return err
	}
	present, err := secrets.NonEmptyDotenvKeys(opts.EnvDeploy)
	if err != nil {
		return fmt.Errorf("smoke: dotenv: %w", err)
	}
	var ran int
	for _, t := range targets {
		if !gatewayconfig.SecretsReady(t, present) {
			miss := gatewayconfig.MissingSecrets(t, present)
			ep.out.Printf("smoke: skip provider backend=%s model=%s (missing %s)\n", t.BackendID, t.Model, strings.Join(miss, ","))
			continue
		}
		if err := runProviderChat(ctx, ep, t.Model, t.ReasoningEffort); err != nil {
			return err
		}
		ran++
	}
	if ran == 0 {
		return fmt.Errorf("smoke: no provider probes ran (check gateway YAML and %s)", opts.EnvDeploy)
	}
	return nil
}

// DefaultSmokeTimeout is the post-deploy inference smoke budget.
const DefaultSmokeTimeout = 300 * time.Second
