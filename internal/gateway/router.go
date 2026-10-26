package gateway

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/outbound"

	"github.com/behaviorengineering/polypus/internal/batch/service"
	"github.com/behaviorengineering/polypus/internal/clients/anthropic"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/gemini"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// registryProvider exposes the routing table.
type registryProvider interface {
	Registry() *router.Registry
}

// chatRouter is the Bifrost/OpenAI chat and embed dial surface (≤4 methods).
type chatRouter interface {
	UsesBifrost(backendID string) bool
	ChatCompletionRaw(ctx context.Context, call router.Call) ([]byte, error)
	ChatCompletionStreamRaw(ctx context.Context, call router.Call) (<-chan router.ChatStreamChunk, <-chan error, error)
	EmbeddingRaw(ctx context.Context, call router.Call) ([]byte, error)
}

// speechRouter is the TTS/STT dial surface (≤2 methods).
type speechRouter interface {
	Synthesize(ctx context.Context, req router.SpeechRequest) ([]byte, error)
	Transcribe(ctx context.Context, req router.TranscriptionRequest) ([]byte, string, error)
}

// systemOneRouter is the Bifrost Decision dial surface for native OpenRouter SystemOne.
type systemOneRouter interface {
	SystemOneDecisionRaw(ctx context.Context, call router.Call) ([]byte, error)
}

// gatewayRouter is the composed gateway dial dependency (Registry + Chat + Speech + SystemOne).
// Prefer depending on the smaller interfaces at call sites; *router.Client implements all.
// Batches run through the batch service, not the router.
type gatewayRouter interface {
	registryProvider
	chatRouter
	speechRouter
	systemOneRouter
}

// routerCloser is optional; owned Bifrost clients implement Close.
type routerCloser interface {
	Close()
}

// cloudflareClientGet constructs extension clients (defaults to cloudflare.GetClient).
type cloudflareClientGet func(config.BackendDef) (*cloudflare.Client, error)

// geminiClientGet constructs Gemini catalog clients (defaults to gemini.GetClient).
type geminiClientGet func(config.BackendDef) (*gemini.Client, error)

// anthropicClientGet constructs Anthropic catalog clients (defaults to anthropic.GetClient).
type anthropicClientGet func(config.BackendDef) (*anthropic.Client, error)

// HandlerOption configures NewHandler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	router         gatewayRouter
	cfGet          cloudflareClientGet
	geminiGet      geminiClientGet
	anthropicGet   anthropicClientGet
	overlayPath    string
	adminKeysPath  string
	clock          func() time.Time
	claudeBatches  service.ClaudeBatches
	policies       *outbound.Policies
	openRouterGet  func(config.BackendDef) (*openrouter.Client, error)
	logger         *slog.Logger
	dumpDir        string
	traceSkipPaths []string
}

// withRouter injects a gatewayRouter so tests can avoid bifrost.Init.
// Panics if r is nil.
func withRouter(r gatewayRouter) HandlerOption {
	if r == nil {
		panic("gateway: WithRouter(nil)")
	}
	return func(o *handlerOptions) {
		o.router = r
	}
}

// withGeminiClientGet injects Gemini catalog client construction for inventory.
func withGeminiClientGet(fn geminiClientGet) HandlerOption {
	return func(o *handlerOptions) {
		o.geminiGet = fn
	}
}

// withAnthropicClientGet injects Anthropic catalog client construction for inventory.
func withAnthropicClientGet(fn anthropicClientGet) HandlerOption {
	return func(o *handlerOptions) {
		o.anthropicGet = fn
	}
}

// WithAdminStateDir sets overlay and admin key paths under dir (tests).
func WithAdminStateDir(dir string) HandlerOption {
	if strings.TrimSpace(dir) == "" {
		panic("gateway: WithAdminStateDir(empty)")
	}
	return func(o *handlerOptions) {
		o.overlayPath = filepath.Join(dir, config.ModelsAllowOverlayFile)
		o.adminKeysPath = filepath.Join(dir, "admin-api-keys.json")
	}
}

// withClaudeBatches sets the Anthropic Message Batches surface the batch service
// uses (tests). By default it comes from the router client. Panics if b is nil.
func withClaudeBatches(b service.ClaudeBatches) HandlerOption {
	if b == nil {
		panic("gateway: WithClaudeBatches(nil)")
	}
	return func(o *handlerOptions) {
		o.claudeBatches = b
	}
}

// WithLogger sets the logger the gateway writes warnings and request failures to.
// By default it is slog.Default(). Panics if l is nil.
func WithLogger(l *slog.Logger) HandlerOption {
	if l == nil {
		panic("gateway: WithLogger(nil)")
	}
	return func(o *handlerOptions) {
		o.logger = l
	}
}

// WithObservability sets where failure dumps land (empty turns them off) and the HTTP
// paths that get no SERVER span (nil keeps the default /health probes).
func WithObservability(dumpDir string, traceSkipPaths []string) HandlerOption {
	return func(o *handlerOptions) {
		o.dumpDir = dumpDir
		o.traceSkipPaths = append([]string(nil), traceSkipPaths...)
	}
}

// fillDefaults resolves every option the caller left unset: state paths, the job
// clock and the provider client getters.
func (o *handlerOptions) fillDefaults() error {
	if o.overlayPath == "" {
		p, err := config.ResolveModelsAllowOverlayPath()
		if err != nil {
			return err
		}
		o.overlayPath = p
	}
	if o.adminKeysPath == "" {
		p, err := config.ResolveAdminKeysPath()
		if err != nil {
			return err
		}
		o.adminKeysPath = p
	}
	if o.clock == nil {
		o.clock = func() time.Time { return time.Now().UTC() }
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	// Each gateway owns its client caches and breakers (no process-wide state),
	// and the router's speech plugin shares the Cloudflare cache.
	if o.policies == nil {
		o.policies = outbound.NewPolicies()
	}
	if o.cfGet == nil {
		o.cfGet = (&cloudflare.Clients{Policies: o.policies, Logger: o.logger}).Get
	}
	if o.geminiGet == nil {
		o.geminiGet = (&gemini.Clients{Policies: o.policies, Logger: o.logger}).Get
	}
	if o.anthropicGet == nil {
		o.anthropicGet = (&anthropic.Clients{Policies: o.policies, Logger: o.logger}).Get
	}
	if o.openRouterGet == nil {
		o.openRouterGet = (&openrouter.Clients{Policies: o.policies}).Get
	}
	return nil
}

var _ gatewayRouter = (*router.Client)(nil)
