package smoke

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/pkg/polypus"

	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
)

const defaultChatModel = polypus.DefaultChatModel

// Options configures homelab post-deploy smoke against a running gateway.
type Options struct {
	BaseURL   string
	ChatModel string
	// WithRouter smokes every chat router in GatewayConfig.
	WithRouter     bool
	WithSystemOne  bool
	SystemOneModel string
	// WithProviders runs chat probes per backend from GatewayConfig (secrets gated via EnvDeploy).
	WithProviders bool
	GatewayConfig string
	EnvDeploy     string
}

// Run executes direct chat smoke and optional router smoke with selected-leaf checks.
func Run(ctx context.Context, opts Options) error {
	if ctx == nil {
		return fmt.Errorf("smoke: context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("smoke: missing deadline")
	}
	opts = opts.withDefaults()
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return fmt.Errorf("smoke: base URL required")
	}

	if opts.WithProviders {
		if err := runProviderSmokes(ctx, base, opts); err != nil {
			return err
		}
	} else if err := runChat(ctx, base, opts.ChatModel, false, nil); err != nil {
		return err
	}
	if opts.WithRouter {
		if err := runRouterSmokes(ctx, base, opts.GatewayConfig); err != nil {
			return err
		}
	}
	if !opts.WithSystemOne {
		return nil
	}
	return runSystemOneProbes(ctx, base, opts)
}

func (o Options) withDefaults() Options {
	out := o
	if strings.TrimSpace(out.BaseURL) == "" {
		out.BaseURL = strings.TrimSpace(os.Getenv("POLYPUS_BASE_URL"))
		if out.BaseURL == "" {
			out.BaseURL = "http://127.0.0.1:1320"
		}
	}
	if strings.TrimSpace(out.ChatModel) == "" {
		out.ChatModel = envOr("POLYPUS_CHAT_SMOKE_MODEL", defaultChatModel)
	}
	if strings.TrimSpace(out.SystemOneModel) == "" {
		out.SystemOneModel = envOr("POLYPUS_SYSTEMONE_SMOKE_MODEL", polypus.DefaultSystemOneModel)
	}
	return out
}

func runChat(ctx context.Context, baseURL, model string, requireSelected bool, allowed []string) error {
	popts := polypus.SmokeOptions{
		BaseURL:               baseURL,
		Channels:              []string{"chat"},
		ChatModel:             model,
		MaxTokens:             64,
		Temperature:           0.2,
		RequireSelectedModel:  requireSelected,
		AllowedSelectedModels: allowed,
	}
	results, err := polypus.Smoke(ctx, popts)
	for _, r := range results {
		fmt.Printf("smoke: channel=%s probe=%s model=%s selected=%s status=%s ms=%d detail=%s\n",
			r.Channel, r.Probe, r.Model, r.SelectedModel, r.Status, r.Ms, r.Detail)
		if r.Status == "fail" {
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
func runRouterSmokes(ctx context.Context, base, gatewayConfig string) error {
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
		if err := runChat(ctx, base, t.Model, true, allowed); err != nil {
			return err
		}
	}
	return nil
}

func runSystemOneProbes(ctx context.Context, baseURL string, opts Options) error {
	if strings.TrimSpace(opts.GatewayConfig) == "" {
		return runSystemOne(ctx, baseURL, opts.SystemOneModel)
	}
	targets, err := gatewayconfig.SystemOneSmokeTargets(opts.GatewayConfig)
	if err != nil {
		return fmt.Errorf("smoke: systemone targets: %w", err)
	}
	if err := gatewayconfig.ValidateSystemOneSmokeTargets(targets); err != nil {
		return err
	}
	if len(targets) == 0 {
		return runSystemOne(ctx, baseURL, opts.SystemOneModel)
	}
	present, err := secrets.NonEmptyDotenvKeys(opts.EnvDeploy)
	if err != nil {
		return fmt.Errorf("smoke: dotenv: %w", err)
	}
	for _, t := range targets {
		if !gatewayconfig.SecretsReady(t, present) {
			miss := gatewayconfig.MissingSecrets(t, present)
			return fmt.Errorf("smoke: systemone backend=%s model=%s missing %s in %s", t.BackendID, t.Model, strings.Join(miss, ","), opts.EnvDeploy)
		}
	}
	for _, t := range targets {
		if err := runSystemOne(ctx, baseURL, t.Model); err != nil {
			return err
		}
	}
	return nil
}

func runSystemOne(ctx context.Context, baseURL, model string) error {
	popts := polypus.SmokeOptions{
		BaseURL:        baseURL,
		Channels:       []string{"systemone"},
		SystemOneModel: model,
	}
	results, err := polypus.Smoke(ctx, popts)
	for _, r := range results {
		fmt.Printf("smoke: channel=%s probe=%s model=%s status=%s ms=%d detail=%s\n",
			r.Channel, r.Probe, r.Model, r.Status, r.Ms, r.Detail)
		if r.Status == "fail" {
			return fmt.Errorf("smoke %s probe %s: %s", model, r.Probe, r.Detail)
		}
	}
	if err != nil {
		return fmt.Errorf("smoke %s: %w", model, err)
	}
	return nil
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func runProviderSmokes(ctx context.Context, base string, opts Options) error {
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
			fmt.Printf("smoke: skip provider backend=%s model=%s (missing %s)\n", t.BackendID, t.Model, strings.Join(miss, ","))
			continue
		}
		if err := runProviderChat(ctx, base, t.Model, t.ReasoningEffort); err != nil {
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
