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
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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

// routerParts is the router split by the handlers that call it: each handler reads
// only the part it needs. *router.Client fills every part.
type routerParts struct {
	registry  *router.Registry
	chat      chatRouter
	speech    speechRouter
	systemOne systemOneRouter
	// bifrost is the router when it is a real Bifrost client, for the Anthropic
	// batches surface; nil for a test fake.
	bifrost *router.Client
	// closeRouter releases a router the gateway built itself; nil when injected.
	closeRouter func()
}

// splitRouter fills the parts r implements; the rest answer CodeUnimplemented.
func splitRouter(r registryProvider) routerParts {
	parts := routerParts{
		registry:  r.Registry(),
		chat:      unsupportedRouter{},
		speech:    unsupportedRouter{},
		systemOne: unsupportedRouter{},
	}
	if c, ok := r.(chatRouter); ok {
		parts.chat = c
	}
	if sp, ok := r.(speechRouter); ok {
		parts.speech = sp
	}
	if so, ok := r.(systemOneRouter); ok {
		parts.systemOne = so
	}
	if client, ok := r.(*router.Client); ok {
		parts.bifrost = client
	}
	return parts
}

// unsupportedRouter stands in for a router part an injected router lacks.
type unsupportedRouter struct{}

func unsupported(op string) error {
	return derrors.New(derrors.CodeUnimplemented, op, "router does not support this call")
}

func (unsupportedRouter) UsesBifrost(string) bool { return false }

func (unsupportedRouter) ChatCompletionRaw(context.Context, router.Call) ([]byte, error) {
	return nil, unsupported("gateway.ChatCompletionRaw")
}

func (unsupportedRouter) ChatCompletionStreamRaw(context.Context, router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	return nil, nil, unsupported("gateway.ChatCompletionStreamRaw")
}

func (unsupportedRouter) EmbeddingRaw(context.Context, router.Call) ([]byte, error) {
	return nil, unsupported("gateway.EmbeddingRaw")
}

func (unsupportedRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, unsupported("gateway.Synthesize")
}

func (unsupportedRouter) Transcribe(context.Context, router.TranscriptionRequest) ([]byte, string, error) {
	return nil, "", unsupported("gateway.Transcribe")
}

func (unsupportedRouter) SystemOneDecisionRaw(context.Context, router.Call) ([]byte, error) {
	return nil, unsupported("gateway.SystemOneDecisionRaw")
}

// cloudflareClientGet returns extension clients (defaults to the gateway's cloudflare.Clients.Get).
type cloudflareClientGet func(config.BackendDef) (*cloudflare.Client, error)

// geminiClientGet returns Gemini catalog clients (defaults to the gateway's gemini.Clients.Get).
type geminiClientGet func(config.BackendDef) (*gemini.Client, error)

// anthropicClientGet returns Anthropic catalog clients (defaults to the gateway's anthropic.Clients.Get).
type anthropicClientGet func(config.BackendDef) (*anthropic.Client, error)

// HandlerOption configures NewHandler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	router         registryProvider
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

// withRouter injects a router so tests can avoid bifrost.Init. The gateway uses the
// chat, speech and SystemOne surfaces r implements. Panics if r is nil.
func withRouter(r registryProvider) HandlerOption {
	if r == nil {
		panic("gateway: withRouter(nil)")
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

// WithAdminStateDir keeps the models allow overlay and the admin key store under
// dir instead of the user config directory. Panics if dir is empty.
func WithAdminStateDir(dir string) HandlerOption {
	if strings.TrimSpace(dir) == "" {
		panic("gateway: WithAdminStateDir(empty)")
	}
	return func(o *handlerOptions) {
		o.overlayPath = filepath.Join(dir, config.ModelsAllowOverlayFile)
		o.adminKeysPath = filepath.Join(dir, config.AdminKeysFile)
	}
}

// withClaudeBatches sets the Anthropic Message Batches surface the batch service
// uses (tests). By default it comes from the router client. Panics if b is nil.
func withClaudeBatches(b service.ClaudeBatches) HandlerOption {
	if b == nil {
		panic("gateway: withClaudeBatches(nil)")
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
		o.cfGet = (&cloudflare.Clients{Policies: o.policies, Logger: o.logger, Now: o.clock, Speech: cloudflare.SpeechDefaultsFromEnv()}).Get
	}
	if o.geminiGet == nil {
		o.geminiGet = (&gemini.Clients{Policies: o.policies, Logger: o.logger, Now: o.clock}).Get
	}
	if o.anthropicGet == nil {
		o.anthropicGet = (&anthropic.Clients{Policies: o.policies, Logger: o.logger, Now: o.clock}).Get
	}
	if o.openRouterGet == nil {
		o.openRouterGet = (&openrouter.Clients{Policies: o.policies}).Get
	}
	return nil
}

var (
	_ chatRouter      = (*router.Client)(nil)
	_ speechRouter    = (*router.Client)(nil)
	_ systemOneRouter = (*router.Client)(nil)
)
