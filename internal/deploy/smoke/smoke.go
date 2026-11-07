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

// Options configures post-deploy smoke against a running gateway.
type Options struct {
	// BaseURL is the gateway; empty reads POLYPUS_BASE_URL. One of them is required.
	BaseURL string
	// ChatModel is the direct chat probe's model when WithProviders is off. Empty
	// reads POLYPUS_CHAT_SMOKE_MODEL, then takes the first chat probe the gateway
	// YAML defines; with neither the smoke fails and says so.
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
	} else if err := runDirectChat(ctx, ep, opts); err != nil {
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
		out.ChatModel = strings.TrimSpace(os.Getenv("POLYPUS_CHAT_SMOKE_MODEL"))
	}
	if strings.TrimSpace(out.SystemOneModel) == "" {
		out.SystemOneModel = config.EnvOr("POLYPUS_SYSTEMONE_SMOKE_MODEL", internalsmoke.DefaultSystemOneModel)
	}
	return out
}

// runDirectChat sends one chat to opts.ChatModel or, with none set, to the first
// chat probe in the gateway YAML whose secrets the deploy has. No compiled-in
// model is assumed: the environment may not define it.
func runDirectChat(ctx context.Context, ep endpoint, opts Options) error {
	if model := strings.TrimSpace(opts.ChatModel); model != "" {
		return runChat(ctx, ep, chatProbe{model: model})
	}
	if strings.TrimSpace(opts.GatewayConfig) == "" {
		return derrors.New(derrors.CodeInvalid, "smoke.run",
			"no chat model to smoke: pass --smoke-chat-model (or set POLYPUS_CHAT_SMOKE_MODEL)")
	}
	targets, err := gatewayconfig.ChatSmokeTargets(opts.GatewayConfig)
	if err != nil {
		return fmt.Errorf("smoke: chat targets: %w", err)
	}
	ready, err := readyTargets(ep, "chat", opts.EnvDeploy, targets)
	if err != nil {
		return err
	}
	if len(ready) == 0 {
		return derrors.New(derrors.CodeInvalid, "smoke.run",
			"no chat model to smoke: give a chat backend a model in "+opts.GatewayConfig+" (models.allow or smoke.chat), or pass --smoke-chat-model")
	}
	return runChat(ctx, ep, chatProbe{model: ready[0].Model, reasoningEffort: ready[0].ReasoningEffort})
}

// chatProbe is one chat smoke: the model, the reasoning effort a reasoning model
// needs to answer within the probe's tokens, and for a router the leaves it may
// answer with.
type chatProbe struct {
	model string
	// reasoningEffort, when set, goes on the request as reasoning.effort.
	reasoningEffort string
	// requireSelected fails the probe when the gateway does not say which model
	// answered.
	requireSelected bool
	// allowed, when set, are the only models that may answer.
	allowed []string
}

func runChat(ctx context.Context, ep endpoint, p chatProbe) error {
	return runProbe(ctx, ep, p.model, internalsmoke.Options{
		Channels:              []string{internalsmoke.ChannelChat},
		ChatModel:             p.model,
		MaxTokens:             64,
		Temperature:           0.2,
		RequireSelectedModel:  p.requireSelected,
		AllowedSelectedModels: p.allowed,
		ReasoningEffort:       p.reasoningEffort,
	})
}

func runSystemOne(ctx context.Context, ep endpoint, model string) error {
	return runProbe(ctx, ep, model, internalsmoke.Options{
		Channels:       []string{internalsmoke.ChannelSystemOne},
		SystemOneModel: model,
	})
}

// runProbe runs one smoke probe for model against ep and prints every result. The
// error is internalsmoke.Run's, so a failed probe's typed cause (a 401, a 429, a
// deadline) stays in the chain.
func runProbe(ctx context.Context, ep endpoint, model string, popts internalsmoke.Options) error {
	popts.BaseURL = ep.url
	popts.APIKey = ep.key
	results, err := internalsmoke.Run(ctx, popts)
	for _, r := range results {
		ep.out.Printf("smoke: channel=%s probe=%s model=%s selected=%s status=%s ms=%d detail=%s\n",
			r.Channel, r.Probe, r.Model, r.SelectedModel, r.Status, r.Ms, r.Detail)
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
		p := chatProbe{model: t.Model, requireSelected: true}
		if t.Composed {
			p.allowed = t.Leaves
		}
		if err := runChat(ctx, ep, p); err != nil {
			return err
		}
	}
	return nil
}

// readyTargets prints a skip line for each target whose secrets are missing from
// envDeploy (the deploy has no key for it, so a probe could only fail) and returns
// the rest. kind names the probe in the skip line.
func readyTargets(ep endpoint, kind, envDeploy string, targets []gatewayconfig.ChatSmokeTarget) ([]gatewayconfig.ChatSmokeTarget, error) {
	present, err := secrets.NonEmptyDotenvKeys(envDeploy)
	if err != nil {
		return nil, fmt.Errorf("smoke: dotenv: %w", err)
	}
	var ready []gatewayconfig.ChatSmokeTarget
	for _, t := range targets {
		if !gatewayconfig.SecretsReady(t, present) {
			miss := gatewayconfig.MissingSecrets(t, present)
			ep.out.Printf("smoke: skip %s backend=%s model=%s (missing %s)\n", kind, t.BackendID, t.Model, strings.Join(miss, ","))
			continue
		}
		ready = append(ready, t)
	}
	return ready, nil
}

func runSystemOneProbes(ctx context.Context, ep endpoint, opts Options) error {
	if strings.TrimSpace(opts.GatewayConfig) == "" {
		return runSystemOne(ctx, ep, opts.SystemOneModel)
	}
	targets, err := gatewayconfig.SystemOneSmokeTargets(opts.GatewayConfig)
	if err != nil {
		return fmt.Errorf("smoke: systemone targets: %w", err)
	}
	if len(targets) == 0 {
		return runSystemOne(ctx, ep, opts.SystemOneModel)
	}
	ready, err := readyTargets(ep, "systemone", opts.EnvDeploy, targets)
	if err != nil {
		return err
	}
	for _, t := range ready {
		if err := runSystemOne(ctx, ep, t.Model); err != nil {
			return err
		}
	}
	if len(ready) == 0 && opts.SystemOneRequested {
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
	ready, err := readyTargets(ep, "provider", opts.EnvDeploy, targets)
	if err != nil {
		return err
	}
	for _, t := range ready {
		if err := runChat(ctx, ep, chatProbe{model: t.Model, reasoningEffort: t.ReasoningEffort}); err != nil {
			return err
		}
	}
	if len(ready) == 0 {
		return fmt.Errorf("smoke: no provider probes ran (check gateway YAML and %s)", opts.EnvDeploy)
	}
	return nil
}

// DefaultSmokeTimeout is the post-deploy inference smoke budget.
const DefaultSmokeTimeout = 300 * time.Second
