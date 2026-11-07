package gateway

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/outbound"

	"github.com/behaviorengineering/polypus/internal/clients/anthropic"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/gemini"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/batches"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// registryProvider exposes the routing table.
type registryProvider interface {
	Registry() *router.Registry
}

// chatRouter is the Bifrost chat and embed dial surface (≤4 methods). A backend it
// does not register fails closed: there is no HTTP fallback.
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

// systemOneRouter is the Bifrost Decision dial surface for SystemOne on native
// OpenRouter and Typesafe backends.
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

// openRouterClientGet returns OpenRouter clients (defaults to the gateway's openrouter.Clients.Get).
type openRouterClientGet func(config.BackendDef) (*openrouter.Client, error)

// backendClients builds the provider clients a backend needs, one getter per
// provider, each over the gateway's own client cache and outbound policies.
type backendClients struct {
	cf         cloudflareClientGet
	gemini     geminiClientGet
	anthropic  anthropicClientGet
	openRouter openRouterClientGet
}

func (c backendClients) cloudflareClient(b config.BackendDef) (*cloudflare.Client, error) {
	return c.cf(b)
}

func (c backendClients) geminiClient(b config.BackendDef) (*gemini.Client, error) {
	return c.gemini(b)
}

func (c backendClients) anthropicClient(b config.BackendDef) (*anthropic.Client, error) {
	return c.anthropic(b)
}

func (c backendClients) openRouterClient(b config.BackendDef) (*openrouter.Client, error) {
	return c.openRouter(b)
}

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
	claudeBatches  batches.ClaudeBatches
	policies       *outbound.Policies
	openRouterGet  openRouterClientGet
	logger         *slog.Logger
	dumpDir        string
	traceSkipPaths []string
	// probeTimeout bounds the backend probes; 0 means backendProbeTimeout.
	probeTimeout time.Duration
	// streamChunkPreview adds redacted chunk previews to stream chunk span events.
	streamChunkPreview bool
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

// WithStreamChunkDebug adds redacted, capped previews of each streamed chat chunk
// to its span event (observability.Config.StreamChunkDebug). Off by default.
func WithStreamChunkDebug(on bool) HandlerOption {
	return func(o *handlerOptions) {
		o.streamChunkPreview = on
	}
}

// withPolicies sets the outbound policies (breakers and retry backoff) the gateway's
// clients share, so tests can retry on a millisecond backoff. By default the gateway
// builds its own production set. Panics if p is nil.
func withPolicies(p *outbound.Policies) HandlerOption {
	if p == nil {
		panic("gateway: withPolicies(nil)")
	}
	return func(o *handlerOptions) {
		o.policies = p
	}
}

// withBackendProbeTimeout sets the budget for the backend probes (/health/backends
// and the startup credential probe), so tests need not wait out the production
// budget against a backend that never answers. Panics if d is not positive.
func withBackendProbeTimeout(d time.Duration) HandlerOption {
	if d <= 0 {
		panic("gateway: withBackendProbeTimeout(non-positive)")
	}
	return func(o *handlerOptions) {
		o.probeTimeout = d
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
func withClaudeBatches(b batches.ClaudeBatches) HandlerOption {
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
	if o.probeTimeout <= 0 {
		o.probeTimeout = backendProbeTimeout
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
