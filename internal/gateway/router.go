package gateway

import (
	"context"
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

// RegistryProvider exposes the routing table.
type RegistryProvider interface {
	Registry() *router.Registry
}

// ChatRouter is the Bifrost/OpenAI chat and embed dial surface (≤4 methods).
type ChatRouter interface {
	UsesBifrost(backendID string) bool
	ChatCompletionRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) ([]byte, error)
	ChatCompletionStreamRaw(ctx context.Context, backendID, model string, body []byte) (<-chan router.ChatStreamChunk, <-chan error, error)
	EmbeddingRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) ([]byte, error)
}

// SpeechRouter is the TTS/STT dial surface (≤2 methods).
type SpeechRouter interface {
	Synthesize(ctx context.Context, req router.SpeechRequest) ([]byte, error)
	Transcribe(ctx context.Context, req router.TranscriptionRequest) ([]byte, string, error)
}

// SystemOneRouter is the Bifrost Decision dial surface for native OpenRouter SystemOne.
type SystemOneRouter interface {
	SystemOneDecisionRaw(ctx context.Context, backendID, downstream string, body []byte, timeout time.Duration) ([]byte, error)
}

// Router is the composed gateway dial dependency (Registry + Chat + Speech + SystemOne).
// Prefer depending on the smaller interfaces at call sites; *router.Client implements all.
// Batches run through the batch service, not the router.
type Router interface {
	RegistryProvider
	ChatRouter
	SpeechRouter
	SystemOneRouter
}

// routerCloser is optional; owned Bifrost clients implement Close.
type routerCloser interface {
	Close()
}

// CloudflareClientGet constructs extension clients (defaults to cloudflare.GetClient).
type CloudflareClientGet func(config.BackendDef) (*cloudflare.Client, error)

// GeminiClientGet constructs Gemini catalog clients (defaults to gemini.GetClient).
type GeminiClientGet func(config.BackendDef) (*gemini.Client, error)

// AnthropicClientGet constructs Anthropic catalog clients (defaults to anthropic.GetClient).
type AnthropicClientGet func(config.BackendDef) (*anthropic.Client, error)

// HandlerOption configures NewHandler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	router        Router
	cfGet         CloudflareClientGet
	geminiGet     GeminiClientGet
	anthropicGet  AnthropicClientGet
	overlayPath   string
	adminKeysPath string
	clock         func() time.Time
	claudeBatches service.ClaudeBatches
	policies      *outbound.Policies
	openRouterGet func(config.BackendDef) (*openrouter.Client, error)
}

// WithRouter injects a Router so tests can avoid bifrost.Init.
// Panics if r is nil.
func WithRouter(r Router) HandlerOption {
	if r == nil {
		panic("gateway: WithRouter(nil)")
	}
	return func(o *handlerOptions) {
		o.router = r
	}
}

// WithCloudflareClientGet injects CF client construction for health/inventory.
func WithCloudflareClientGet(fn CloudflareClientGet) HandlerOption {
	return func(o *handlerOptions) {
		o.cfGet = fn
	}
}

// WithGeminiClientGet injects Gemini catalog client construction for inventory.
func WithGeminiClientGet(fn GeminiClientGet) HandlerOption {
	return func(o *handlerOptions) {
		o.geminiGet = fn
	}
}

// WithAnthropicClientGet injects Anthropic catalog client construction for inventory.
func WithAnthropicClientGet(fn AnthropicClientGet) HandlerOption {
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
		o.overlayPath = filepath.Join(dir, "models-allow-overlay.yaml")
		o.adminKeysPath = filepath.Join(dir, "admin-api-keys.json")
	}
}

// WithClaudeBatches sets the Anthropic Message Batches surface the batch service
// uses (tests). By default it comes from the router client. Panics if b is nil.
func WithClaudeBatches(b service.ClaudeBatches) HandlerOption {
	if b == nil {
		panic("gateway: WithClaudeBatches(nil)")
	}
	return func(o *handlerOptions) {
		o.claudeBatches = b
	}
}

// WithClock sets the job clock that stamps admin keys and batch records (tests).
// Panics if now is nil.
func WithClock(now func() time.Time) HandlerOption {
	if now == nil {
		panic("gateway: WithClock(nil)")
	}
	return func(o *handlerOptions) {
		o.clock = now
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
	// Each gateway owns its client caches and breakers (no process-wide state),
	// and the router's speech plugin shares the Cloudflare cache.
	if o.policies == nil {
		o.policies = outbound.NewPolicies()
	}
	if o.cfGet == nil {
		o.cfGet = (&cloudflare.Clients{Policies: o.policies}).Get
	}
	if o.geminiGet == nil {
		o.geminiGet = (&gemini.Clients{Policies: o.policies}).Get
	}
	if o.anthropicGet == nil {
		o.anthropicGet = (&anthropic.Clients{Policies: o.policies}).Get
	}
	if o.openRouterGet == nil {
		o.openRouterGet = (&openrouter.Clients{Policies: o.policies}).Get
	}
	return nil
}

var _ Router = (*router.Client)(nil)
