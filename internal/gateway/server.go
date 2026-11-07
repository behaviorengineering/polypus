package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/openaicompat"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/batches"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// gatewayHandler is the Polypus HTTP mux (controller) over capability handlers.
// Each capability handler holds only the dependencies its routes use;
// createShared builds them all from config.
type gatewayHandler struct {
	responder
	registry *router.Registry
	// proxy serves the voices list from the proxy backend.
	proxy    http.Handler
	uiMounts []uiProxyMount
	// clients is for the startup credential probe.
	clients backendClients
	// closeRouter releases a router the gateway built itself; nil when injected.
	closeRouter func()
	// closeOnce makes Close safe to call more than once.
	closeOnce sync.Once
	// traceSkipPaths are HTTP paths that get no SERVER span.
	traceSkipPaths []string

	chat      chatHandler
	speech    speechHandler
	systemOne systemOneHandler
	models    modelsHandler
	health    healthHandler
	catalog   apiCatalogHandler
	files     filesHandler
	batches   batchesHandler
	admin     adminHandler
}

// chatHandler serves chat completions, named routers and embeddings.
type chatHandler struct {
	responder
	registry  *router.Registry
	chat      chatRouter
	upstreams *upstream.Board
	timeouts  config.Timeouts
	// dumps is where streams dump the chunks they drop.
	dumps chunkDumpSink
}

// speechHandler serves text to speech and transcription.
type speechHandler struct {
	responder
	registry  *router.Registry
	speech    speechRouter
	upstreams *upstream.Board
	timeouts  config.Timeouts
}

// modelsHandler serves the model lists and keeps the inventory cache.
type modelsHandler struct {
	responder
	registry *router.Registry
	cache    *modelsInventoryCache
	// inventory is the models warm-up, refresh and cache settings.
	inventory config.ModelsInventory
	clients   backendClients
	upstreams *upstream.Board
	compat    openaicompat.Client
}

// healthHandler serves the health probes and the failure dumps.
type healthHandler struct {
	responder
	registry  *router.Registry
	upstreams *upstream.Board
	compat    openaicompat.Client
	clients   backendClients
	// dumpDir receives failure dumps the debug route serves; empty turns them off.
	dumpDir string
	// switchyardEnabled is POLYPUS_SWITCHYARD, read once when the gateway is built.
	switchyardEnabled bool
	// probeTimeout bounds one round of backend probes.
	probeTimeout time.Duration
}

// Handler is the public Polypus HTTP handler. Close releases the router it built
// (Bifrost workers and pooled connections): call it once the server that serves the
// handler has shut down, or when the host replaces the handler, for example on a
// config reload. Requests still in flight when Close runs may fail.
type Handler interface {
	http.Handler
	io.Closer
}

// NewHandler returns the public Polypus HTTP handler. The caller owns it and calls
// Close when done with it. It does not write Switchyard TOML; ListenAndServe
// (process startup) does. Tests pass withRouter to inject a fake and skip
// bifrost.Init.
//
// NewHandler is a process entry for hosts that only want the handler, so it starts
// the router with a fresh context; ListenAndServe passes its own startup context.
func NewHandler(opts config.ServeOptions, options ...HandlerOption) (Handler, error) {
	return newHandler(context.Background(), opts, options...)
}

// newHandler is NewHandler with the startup context the router's Bifrost runs under.
func newHandler(ctx context.Context, opts config.ServeOptions, options ...HandlerOption) (Handler, error) {
	const op = "gateway.NewHandler"
	var ho handlerOptions
	for _, opt := range options {
		if opt != nil {
			opt(&ho)
		}
	}
	if err := ho.fillDefaults(); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "handler options")
	}
	adminKeys, err := keys.Config{Path: ho.adminKeysPath, Clock: ho.clock}.CreateStore()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "admin keys")
	}
	parts, rcfg, err := openRouter(ctx, opts, ho)
	if err != nil {
		return nil, err
	}
	g, err := sharedConfig{
		options:   ho,
		router:    parts,
		routerCfg: rcfg,
	}.createShared()
	if err != nil {
		if parts.closeRouter != nil {
			parts.closeRouter()
		}
		return nil, err
	}
	return wrapGatewayAccess(adminKeys, g), nil
}

// openRouter returns the injected router, or loads the router config, applies the
// allow overlay and builds a Bifrost client that the gateway then owns.
func openRouter(ctx context.Context, opts config.ServeOptions, ho handlerOptions) (routerParts, config.RouterConfig, error) {
	const op = "gateway.openRouter"
	if ho.router != nil {
		parts := splitRouter(ho.router)
		return parts, parts.registry.Config(), nil
	}
	loaded, err := config.LoadRouterConfig(opts)
	if err != nil {
		return routerParts{}, config.RouterConfig{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "load router config")
	}
	loaded.LogStartup(ho.logger)
	skipped, err := config.ApplyAllowOverlay(&loaded, ho.overlayPath)
	if err != nil {
		return routerParts{}, config.RouterConfig{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "allow overlay")
	}
	for _, s := range skipped {
		ho.logger.Warn("polypus: allow overlay backend skipped", "backend", s.Backend, "reason", s.Reason)
	}
	// fillDefaults always sets cfGet, so the router's speech plugin shares the
	// gateway's Cloudflare client cache.
	client, err := router.NewClient(ctx, loaded, router.WithLogger(ho.logger),
		router.WithCloudflareClientGet(router.CloudflareClientGet(ho.cfGet)))
	if err != nil {
		return routerParts{}, config.RouterConfig{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "bifrost client")
	}
	parts := splitRouter(client)
	parts.closeRouter = client.Close
	return parts, loaded, nil
}

// sharedConfig is everything createShared needs to build one gateway.
type sharedConfig struct {
	options   handlerOptions
	router    routerParts
	routerCfg config.RouterConfig
}

// createShared is the one assembly point: it builds the state the handlers share
// (breakers, timeouts, client getters, batch store, models cache, fallback proxy and
// UI mounts) and hands each capability handler the part it uses. The caller closes
// an owned router when it fails. options must have been through fillDefaults.
func (c sharedConfig) createShared() (*gatewayHandler, error) {
	const op = "gateway.createShared"
	ho := c.options
	if ho.clock == nil || ho.logger == nil || ho.policies == nil || c.router.registry == nil {
		panic("gateway: createShared needs a clock, a logger, outbound policies and a router")
	}
	timeouts := c.routerCfg.Timeouts
	if timeouts.Max == 0 {
		timeouts = config.DefaultTimeouts()
	}
	batchRoot, err := config.ResolveBatchDir()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "batch dir")
	}
	batchStore, err := batch.NewDiskStore(batchRoot, ho.clock)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "batch store")
	}
	inventory, err := config.LoadModelsInventory()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "models inventory")
	}
	rs := responder{log: ho.logger}
	reg := c.router.registry
	upstreams := upstream.NewBoard()
	clients := backendClients{cf: ho.cfGet, gemini: ho.geminiGet, anthropic: ho.anthropicGet, openRouter: ho.openRouterGet}
	compat := openaicompat.Client{HTTP: tracedProbeClient, Policies: ho.policies}

	proxy, err := newFallbackProxy(rs, reg.ProxyBackendURL())
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "fallback proxy")
	}
	uiMounts, err := buildUIMounts(rs, c.routerCfg.UIProxies)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "ui proxies")
	}
	submitters := batchSubmitters{claude: ho.claudeBatches, clients: clients, clock: ho.clock}
	if c.router.bifrost != nil && submitters.claude == nil {
		submitters.claude = router.NewClaudeBatches(c.router.bifrost)
	}
	batchService, err := batches.Config{
		Store:     batchStore,
		Breakers:  upstreams,
		Now:       ho.clock,
		Submitter: submitters.submitter,
		Logger:    ho.logger,
	}.CreateService()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), op, "batch service")
	}

	models := modelsHandler{
		responder: rs, registry: reg, cache: newModelsInventoryCache(ho.logger, inventory.CacheTTL, ho.clock),
		inventory: inventory, clients: clients, upstreams: upstreams, compat: compat,
	}
	return &gatewayHandler{
		responder:      rs,
		registry:       reg,
		proxy:          proxy,
		uiMounts:       uiMounts,
		clients:        clients,
		closeRouter:    c.router.closeRouter,
		traceSkipPaths: ho.traceSkipPaths,
		chat: chatHandler{
			responder: rs, registry: reg, chat: c.router.chat, upstreams: upstreams, timeouts: timeouts,
			dumps: chunkDumpSink{preview: ho.streamChunkPreview, dir: ho.dumpDir, log: ho.logger, now: ho.clock},
		},
		speech: speechHandler{responder: rs, registry: reg, speech: c.router.speech, upstreams: upstreams, timeouts: timeouts},
		systemOne: systemOneHandler{
			responder: rs, registry: reg, systemOne: c.router.systemOne, clients: clients, upstreams: upstreams, timeouts: timeouts,
		},
		models: models,
		health: healthHandler{
			responder: rs, registry: reg, upstreams: upstreams, compat: compat, clients: clients,
			dumpDir: ho.dumpDir, switchyardEnabled: config.SwitchyardEnabled(), probeTimeout: ho.probeTimeout,
		},
		catalog: apiCatalogHandler{responder: rs, registry: reg},
		files:   filesHandler{responder: rs, batchStore: batchStore},
		batches: batchesHandler{responder: rs, registry: reg, batchStore: batchStore, batches: batchService, timeouts: timeouts},
		admin:   adminHandler{responder: rs, registry: reg, overlayPath: ho.overlayPath, models: models},
	}, nil
}

// batchSubmitters picks the batch submitter for a backend.
type batchSubmitters struct {
	// claude is the Anthropic Message Batches surface; nil without a Bifrost router.
	claude  batches.ClaudeBatches
	clients backendClients
	// clock is the gateway's job clock.
	clock func() time.Time
}

func (s batchSubmitters) submitter(b config.BackendDef) (batches.Submitter, error) {
	switch {
	case b.IsAnthropicExtension():
		if s.claude == nil {
			return nil, derrors.New(derrors.CodeNotReady, "gateway.batchSubmitter", "claude batches need the bifrost router").
				With("backend", b.ID)
		}
		return batches.NewClaudeSubmitter(s.claude, b), nil
	case b.IsOpenRouter():
		return batches.NewOpenRouterSubmitter(func() (*openrouter.Client, error) { return s.clients.openRouterClient(b) }), nil
	case b.IsCloudflareExtension():
		return batches.NewCloudflareSubmitter(func() (*cloudflare.Client, error) { return s.clients.cloudflareClient(b) }, s.clock), nil
	default:
		return nil, derrors.New(derrors.CodeFailedPrecondition, "gateway.batchSubmitter",
			"batch only supported for cloudflare, openrouter and anthropic backends").With("backend", b.ID)
	}
}

// newFallbackProxy proxies to the proxy backend, answering a failed dial through rs.
func newFallbackProxy(rs responder, backendURL string) (http.Handler, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "gateway.newFallbackProxy", "backend url").
			With("url", backendURL)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			rs.writeErrorJSON(w, r, http.StatusBadGateway,
				derrors.Wrap(err, derrors.CodeUnavailable, "gateway.backendProxy", "polypus backend unavailable"), "")
		},
	}
	return proxy, nil
}

func (g *gatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, rt := range gatewayRoutes {
		if rt.matches(r) {
			rt.serve(g, w, r)
			return
		}
	}
	if g.serveUIProxy(w, r) {
		return
	}
	http.NotFound(w, r)
}

// routeMethods is which methods a route answers.
type routeMethods int

const (
	methodsRead routeMethods = iota // GET and HEAD.
	methodsGet                      // GET only.
	methodsPost                     // POST only.
	methodsAny                      // The handler checks the method itself.
)

// gatewayRoute is one entry of the routing table. Routes are tried in order, so a
// prefix route must come after the exact routes it would otherwise shadow.
type gatewayRoute struct {
	path    string
	prefix  bool
	methods routeMethods
	serve   func(g *gatewayHandler, w http.ResponseWriter, r *http.Request)
}

func (rt gatewayRoute) matches(r *http.Request) bool {
	if rt.prefix {
		if !strings.HasPrefix(r.URL.Path, rt.path) {
			return false
		}
	} else if r.URL.Path != rt.path {
		return false
	}
	switch rt.methods {
	case methodsRead:
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	case methodsGet:
		return r.Method == http.MethodGet
	case methodsPost:
		return r.Method == http.MethodPost
	case methodsAny:
		return true
	default:
		return false
	}
}

var gatewayRoutes = []gatewayRoute{
	{path: "/", methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) { g.serveLanding(w, r) }},
	{path: bannerAssetPath, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) { g.serveBannerWebP(w, r) }},
	{path: pathHealth, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.health.serveHealth(w, r)
	}},
	{path: pathHealthBackends, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.health.serveBackendHealth(w, r)
	}},
	{path: pathHealthUpstreams, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.health.serveUpstreamHealth(w, r)
	}},
	{path: pathDebugFailures, prefix: true, methods: methodsGet, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.health.serveFailureDump(w, r)
	}},
	{path: pathAPIs, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.catalog.serveCatalog(w, r)
	}},
	{path: pathOpenAISpec, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) { g.serveOpenAPISpec(w, r) }},
	{path: pathSystemOneSchema, methods: methodsRead, serve: (*gatewayHandler).serveSystemOneSchemaRoute},
	{path: pathOpenAIModels, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.models.serveModelsListForSurface(surfaceOpenAI, w, r)
	}},
	{path: pathOpenAIModels + "/", prefix: true, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.models.serveModelRetrieveForSurface(surfaceOpenAI, pathOpenAIModels+"/", w, r)
	}},
	{path: pathSystemOneModels, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.models.serveModelsListForSurface(surfaceSystemOne, w, r)
	}},
	{path: pathSystemOneModels + "/", prefix: true, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.models.serveModelRetrieveForSurface(surfaceSystemOne, pathSystemOneModels+"/", w, r)
	}},
	{path: pathModels, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.models.serveModelsList(w, r)
	}},
	{path: pathModelsPrefix, prefix: true, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.models.serveModelRetrieve(w, r)
	}},
	{path: pathChatCompletions, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.chat.serveChatCompletions(w, r)
	}},
	{path: pathEmbeddings, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.chat.serveEmbeddings(w, r)
	}},
	{path: pathAudioSpeech, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.speech.serveSpeech(w, r)
	}},
	{path: pathAudioTranscriptions, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.speech.serveTranscription(w, r)
	}},
	{path: pathAudioVoices, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) { g.proxy.ServeHTTP(w, r) }},
	{path: pathSystemOne, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.systemOne.serveSystemOne(w, r)
	}},
	{path: pathFiles, prefix: true, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.files.serveFiles(w, r)
	}},
	{path: pathBatches, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.batches.serveBatches(w, r)
	}},
	{path: pathBatchesPrefix, prefix: true, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		g.batches.serveBatchByID(w, r)
	}},
	{path: pathAdminModelsAllow, methods: methodsAny, serve: (*gatewayHandler).serveModelsAllowRoute},
}

func (g *gatewayHandler) serveSystemOneSchemaRoute(w http.ResponseWriter, r *http.Request) {
	if !systemOneAPIEnabled(g.registry.Config()) {
		g.writeAPINotConfigured(w)
		return
	}
	g.serveSystemOneSchema(w, r)
}

func (g *gatewayHandler) serveModelsAllowRoute(w http.ResponseWriter, r *http.Request) {
	h := g.admin
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.serveModelsAllowOverlay(w, r)
	case http.MethodPost:
		h.serveModelsAllow(w, r)
	default:
		h.writeMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPost)
	}
}

func (h chatHandler) serveChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, chatMaxBody))
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveChatCompletions", "read body"))
		return
	}
	req, err := parseChatRequest(body)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	model := req.model

	reg := h.registry
	cfg := reg.Config()

	if routerName, ok := parseNamedRouterModel(model); ok {
		h.serveNamedRouterChat(w, r, req, routerName)
		return
	}

	var backendID, downstream string
	if req.vision {
		backendID, downstream, err = reg.ResolveVision(model)
	} else {
		if cfg.EffectiveChatBackend() == "" {
			h.writeHandlerError(w, r, derrors.New(derrors.CodeNotReady, "gateway.serveChatCompletions", "no chat backend configured"))
			return
		}
		backendID, downstream, err = reg.ResolveChat(model)
	}
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "gateway.serveChatCompletions", "resolve backend"))
		return
	}
	if !ensureModelAllowed(h.registry, backendID, model) {
		h.writeModelNotAllowed(w, model)
		return
	}
	t, err := resolveTarget(h.registry, "gateway.serveChatCompletions", backendID, downstream)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	h.dialChat(w, r, req, t)
}

func (h chatHandler) serveNamedRouterChat(w http.ResponseWriter, r *http.Request, req chatRequest, routerName string) {
	model := req.model
	reg := h.registry
	cfg := reg.Config()
	nr, ok := cfg.LookupNamedRouter(routerName)
	if !ok {
		h.writeHandlerError(w, r, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unknown router").
			With(keyModel, model).
			With("router", routerName))
		return
	}
	if msg := validateNamedRouterForChat(nr, model, req.vision); msg != "" {
		h.writeHandlerError(w, r, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", msg))
		return
	}

	switch classifyNamedRouterRoute(nr.Route.Type) {
	case dispatchPassthrough:
		h.servePassthroughRouterChat(w, r, req, nr.Route.Target)
	case dispatchSwitchyard:
		// Switchyard picks the leaf itself, so it gets the body as the client sent it.
		h.serveSwitchyardRouterChat(w, r, req, routerName)
	case dispatchUnknown:
		fallthrough
	default:
		h.writeHandlerError(w, r, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unsupported route type").
			With(keyModel, model).
			With("router", routerName))
	}
}

func (h chatHandler) servePassthroughRouterChat(w http.ResponseWriter, r *http.Request, req chatRequest, target string) {
	backendID, downstream, err := h.registry.ResolveChat(target)
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "gateway.servePassthroughRouterChat", "resolve target"))
		return
	}
	if !ensureModelAllowed(h.registry, backendID, target) {
		h.writeModelNotAllowed(w, target)
		return
	}
	t, err := resolveTarget(h.registry, "gateway.servePassthroughRouterChat", backendID, downstream)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	h.dialChat(w, r, req, t)
}

func (h chatHandler) serveSwitchyardRouterChat(w http.ResponseWriter, r *http.Request, req chatRequest, routerName string) {
	switchyardURL := h.registry.Config().EffectiveSwitchyardBaseURL()
	model := req.model
	var err error
	ctx, span := observability.StartRouterSpan(r.Context(), "polypus.router", observability.LLMSpanAttrs{
		Model: model, BackendURL: switchyardURL, Router: routerName,
	})
	dialUpstream := upstream.NameSwitchyard
	defer func() { endDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)
	// Switchyard is a Bifrost provider like any leaf, so the hop goes through it with
	// the body as the client sent it; the model names the composed router.
	c := chatCall{
		target:                    backendTarget{id: router.ProviderSwitchyard, downstream: routerName, url: switchyardURL},
		body:                      req.raw,
		stream:                    req.stream,
		hop:                       hopBudget(h.timeouts.Max, config.DefaultTimeouts().Max),
		recordRouterSelectedModel: true,
	}
	if err = h.requireBifrost("gateway.serveSwitchyardRouterChat", router.ProviderSwitchyard); err == nil {
		err = h.upstreams.Execute(r.Context(), upstream.NameSwitchyard, func() error { return h.bifrostChat(w, r, c) })
	}
	if err != nil {
		// A 4xx is Switchyard's answer about the request, not an outage.
		prefix := "polypus: switchyard unavailable: "
		if derrors.HTTPStatus(err) < http.StatusInternalServerError {
			prefix = ""
		}
		h.writeUpstreamDialError(w, r, dialFailure{
			err:         err,
			prefix:      prefix,
			upstream:    upstream.NameSwitchyard,
			unreachable: isSwitchyardUnreachable,
		})
	}
}

// isSwitchyardUnreachable reports a dial that never reached Switchyard: refused, reset
// or an unknown host.
func isSwitchyardUnreachable(err error) bool {
	var dnsErr *net.DNSError
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.As(err, &dnsErr)
}

// writeHandlerError writes a domain error (or any error) using HTTPStatus.
func (rs responder) writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	if rs.writeRateLimitError(w, err) {
		return
	}
	if upstream.Unavailable(err) {
		name := upstream.ResolveUpstreamName(err, "")
		rs.log.WarnContext(r.Context(), "polypus: upstream unavailable", requestLogAttrs(r, "upstream", name, "err", err)...)
		rs.writeUnavailableError(w, err, derrors.PublicMessage(err), name)
		return
	}
	status := derrors.HTTPStatus(err)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	rs.writeErrorJSON(w, r, status, err, "")
}

// writeErrorJSON answers with an OpenAI-shaped error body. The client sees the
// public message; the full chain (ops, upstream URLs, causes) goes to the log.
func (rs responder) writeErrorJSON(w http.ResponseWriter, r *http.Request, status int, err error, prefix string) {
	if status >= http.StatusInternalServerError {
		rs.log.WarnContext(r.Context(), "polypus: request failed", requestLogAttrs(r, "status", status, "err", err)...)
	}
	body := openaiErrorBody{Error: openaiError{
		Message: prefix + derrors.PublicMessage(err),
		Type:    openaiErrorType(status),
		Code:    string(derrors.CodeOf(err)),
	}}
	if intervention := derrors.Field(err, derrors.FieldIntervention); intervention != "" {
		joinInterventionHeader(w, intervention)
		body.Polypus = &polypusFailureBody{Intervention: intervention}
	}
	rs.writeJSON(w, status, body)
}

// requestLogAttrs puts the request's method, path and trace id before attrs, so an
// operator can tie a failure log line to its route and its trace.
func requestLogAttrs(r *http.Request, attrs ...any) []any {
	out := make([]any, 0, len(attrs)+6)
	out = append(out, "method", r.Method, "path", r.URL.Path)
	if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
		out = append(out, "trace_id", sc.TraceID().String())
	}
	return append(out, attrs...)
}

// openaiErrorType maps a status to the OpenAI error type clients switch on.
func openaiErrorType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return errTypeAuthentication
	case status == http.StatusNotFound:
		return errTypeNotFound
	case status < http.StatusInternalServerError:
		return errTypeInvalidRequest
	default:
		return errTypeAPI
	}
}

// dialFailure is one failed upstream dial to answer the client for.
type dialFailure struct {
	err error
	// prefix is prepended to the client message (Switchyard-style messages).
	prefix string
	// upstream names the dependency that failed.
	upstream string
	// unreachable, when set, maps a dial that never reached the upstream to 503.
	unreachable func(error) bool
}

// writeUpstreamDialError writes a dial failure unless the upstream body was already sent.
// Typed domain errors use HTTPStatus; other errors stay 502 unless the breaker or unreachable hook says 503.
func (rs responder) writeUpstreamDialError(w http.ResponseWriter, r *http.Request, f dialFailure) {
	err, prefix, upstreamName, unreachable := f.err, f.prefix, f.upstream, f.unreachable
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	if rs.writeRateLimitError(w, err) {
		return
	}
	if upstream.Unavailable(err) || (unreachable != nil && unreachable(err)) {
		rs.log.WarnContext(r.Context(), "polypus: upstream unavailable", requestLogAttrs(r, "upstream", upstreamName, "err", err)...)
		rs.writeUnavailableError(w, err, prefix+derrors.PublicMessage(err), upstreamName)
		return
	}
	code := http.StatusBadGateway
	var de *derrors.Error
	if errors.As(err, &de) {
		code = derrors.HTTPStatus(err)
	}
	rs.writeErrorJSON(w, r, code, err, prefix)
}

func (h chatHandler) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	const op = "gateway.serveEmbeddings"
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, embedMaxBody))
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, op, "read body"))
		return
	}
	model, err := bodyModel(op, body, true)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.embeddings", observability.LLMSpanAttrs{Model: model})
	var dialUpstream string
	// Every exit below sets err, so the span ends as what the client got.
	defer func() { endDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)

	call, err := h.resolveEmbeddings(r, model, body)
	if errors.Is(err, errModelNotAllowed) {
		h.writeModelNotAllowed(w, model)
		return
	}
	if err != nil {
		recordInterventionFromError(ctx, err)
		h.writeHandlerError(w, r, err)
		return
	}
	span.SetAttributes(
		attribute.String("polypus.backend_id", call.target.id),
		attribute.String("polypus.backend_url", call.target.url),
		attribute.String("polypus.downstream_model", call.target.downstream),
	)
	dialUpstream = call.target.id
	if err = h.dialEmbeddings(w, r, call); err != nil {
		h.writeUpstreamDialError(w, r, dialFailure{err: err, upstream: call.target.id})
	}
}

// errModelNotAllowed marks a request the models.allow gate refused; the handler
// answers it with the OpenAI not-allowed body.
var errModelNotAllowed = errors.New("model not allowed")

// embedCall is one resolved embeddings request.
type embedCall struct {
	target backendTarget
	body   []byte
	hop    time.Duration
	// callerShorter is set when the caller's X-Polypus-Timeout cut the hop budget.
	callerShorter bool
}

// resolveEmbeddings picks the backend for model, applies the allow gate and encodes
// the body for the backend.
func (h chatHandler) resolveEmbeddings(r *http.Request, model string, body []byte) (embedCall, error) {
	const op = "gateway.resolveEmbeddings"
	if h.registry.Config().EffectiveEmbedBackend() == "" {
		return embedCall{}, derrors.New(derrors.CodeNotReady, op, "no embed backend configured").
			With(derrors.FieldIntervention, string(observability.InterventionEmbedCapabilityMissing))
	}
	backendID, downstream, err := h.registry.ResolveEmbed(model)
	if err != nil {
		return embedCall{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "resolve backend")
	}
	if !ensureModelAllowed(h.registry, backendID, model) {
		return embedCall{}, derrors.Wrap(errModelNotAllowed, derrors.CodeInvalid, op, "model not allowed").With(keyModel, model)
	}
	t, err := resolveTarget(h.registry, op, backendID, downstream)
	if err != nil {
		return embedCall{}, err
	}
	rewritten, err := withBodyModel(op, body, downstream)
	if err != nil {
		return embedCall{}, err
	}
	hop := hopBudget(h.timeouts.ResolveEmbed(r.Header.Get(config.TimeoutHeader)), config.DefaultTimeouts().Embed)
	configured := hopBudget(h.timeouts.ResolveEmbed(""), config.DefaultTimeouts().Embed)
	return embedCall{target: t, body: rewritten, hop: hop, callerShorter: hop < configured}, nil
}

// dialEmbeddings sends c through Bifrost under its backend's breaker and writes the
// answer.
func (h chatHandler) dialEmbeddings(w http.ResponseWriter, r *http.Request, c embedCall) error {
	const op = "gateway.dialEmbeddings"
	if err := h.requireBifrost(op, c.target.id); err != nil {
		return err
	}
	ctx := r.Context()
	if c.callerShorter {
		ctx = config.WithCallerBudget(ctx)
	}
	return h.upstreams.Execute(ctx, c.target.id, func() error {
		raw, err := h.chat.EmbeddingRaw(ctx, router.Call{BackendID: c.target.id, Model: c.target.downstream, Body: c.body, Timeout: c.hop})
		if err != nil {
			return gatewayErr(err, op, "embedding").With("backend", c.target.id)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(raw); err != nil {
			return afterHeaders(r.Context(), err, true, op, "write response")
		}
		return nil
	})
}

// transcriptionMaxMemory is how much of a multipart upload stays in memory; the rest
// spills to temp files.
const transcriptionMaxMemory = 32 << 20

// Request body caps. Past them MaxBytesReader fails the read and writeHandlerError
// answers 413.
const (
	speechMaxBody        = 1 << 20  // TTS JSON; OpenAI caps input at 4096 characters.
	transcriptionMaxBody = 26 << 20 // OpenAI's 25 MB audio cap plus multipart overhead.
)

func (h speechHandler) serveSpeech(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, speechMaxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSpeech", "read body"))
		return
	}
	var req struct {
		Model          string   `json:"model"`
		Input          string   `json:"input"`
		Voice          string   `json:"voice"`
		ResponseFormat string   `json:"response_format"`
		Speed          *float64 `json:"speed"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSpeech", "invalid json"))
		return
	}
	backendID, downstream, resolveErr := h.registry.ResolveTTS(req.Model)
	if resolveErr != nil {
		h.writeHandlerError(w, r, derrors.Wrap(resolveErr, derrors.CodeOr(resolveErr, derrors.CodeInvalid), "gateway.serveSpeech", "resolve backend").
			With(keyModel, req.Model))
		return
	}
	if strings.TrimSpace(req.Model) != "" && !ensureModelAllowed(h.registry, string(backendID), req.Model) {
		h.writeModelNotAllowed(w, req.Model)
		return
	}
	h.speechDial(w, r, speechHop{
		span: "polypus.speech", op: "gateway.serveSpeech",
		model: req.Model, backendID: string(backendID), downstream: downstream,
	}, func(ctx context.Context) ([]byte, string, error) {
		audio, err := h.speech.Synthesize(ctx, router.SpeechRequest{
			Model:          req.Model,
			Input:          req.Input,
			Voice:          req.Voice,
			ResponseFormat: req.ResponseFormat,
			Speed:          req.Speed,
		})
		return audio, speechContentType(req.ResponseFormat), err
	})
}

func (h speechHandler) serveTranscription(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, transcriptionMaxBody)
	if err := r.ParseMultipartForm(transcriptionMaxMemory); err != nil { //nolint:gosec // G120: MaxBytesReader above caps the body
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "multipart"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "file required"))
		return
	}
	defer func() { _ = file.Close() }()
	audio, err := io.ReadAll(file)
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "read file"))
		return
	}
	filename := "audio.wav"
	if header != nil && header.Filename != "" {
		filename = header.Filename
	}
	format := r.FormValue("response_format")
	if format == "" {
		format = "json"
	}
	sttModel := r.FormValue(keyModel)
	backendID, downstream, resolveErr := h.registry.ResolveSTT(sttModel)
	if resolveErr != nil {
		h.writeHandlerError(w, r, derrors.Wrap(resolveErr, derrors.CodeOr(resolveErr, derrors.CodeInvalid), "gateway.serveTranscription", "resolve backend").
			With(keyModel, sttModel))
		return
	}
	if strings.TrimSpace(sttModel) != "" && !ensureModelAllowed(h.registry, string(backendID), sttModel) {
		h.writeModelNotAllowed(w, sttModel)
		return
	}
	h.speechDial(w, r, speechHop{
		span: "polypus.transcription", op: "gateway.serveTranscription",
		model: sttModel, backendID: string(backendID), downstream: downstream,
	}, func(ctx context.Context) ([]byte, string, error) {
		return h.speech.Transcribe(ctx, router.TranscriptionRequest{
			Model:          sttModel,
			Audio:          audio,
			Filename:       filename,
			ResponseFormat: format,
			Language:       r.FormValue("language"),
		})
	})
}

// speechHop names one resolved speech or transcription call.
type speechHop struct {
	span       string
	op         string
	model      string
	backendID  string
	downstream string
}

// speechDial runs one speech hop for its backend under its span, budget and breaker,
// then writes the body with its content type, or the dial error.
func (h speechHandler) speechDial(w http.ResponseWriter, r *http.Request, hop speechHop,
	call func(ctx context.Context) (body []byte, contentType string, err error),
) {
	var err error
	backendURL, _ := h.registry.BackendURL(hop.backendID)
	ctx, span := observability.StartLLMSpan(r.Context(), hop.span, observability.LLMSpanAttrs{
		Model: hop.model, BackendID: hop.backendID, BackendURL: backendURL, Downstream: hop.downstream,
	})
	defer func() { endDialSpan(span, err, hop.backendID) }()
	budget := hopBudget(h.timeouts.ResolveSpeech(r.Header.Get(config.TimeoutHeader)), config.DefaultTimeouts().Speech)
	ctx = callerBudget(ctx, budget, hopBudget(h.timeouts.ResolveSpeech(""), config.DefaultTimeouts().Speech))
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var body []byte
	var contentType string
	err = h.upstreams.Execute(ctx, hop.backendID, func() error {
		var callErr error
		body, contentType, callErr = call(ctx)
		return callErr
	})
	if err != nil {
		h.writeUpstreamDialError(w, r, dialFailure{err: err, upstream: hop.backendID})
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(body); writeErr != nil {
		err = derrors.Wrap(writeErr, derrors.CodeInternal, hop.op, "write")
	}
}

func speechContentType(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "wav":
		return "audio/wav"
	case "opus":
		return "audio/opus"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	default:
		return "audio/mpeg"
	}
}

// callerBudget marks ctx with config.WithCallerBudget when the caller's
// X-Polypus-Timeout cut hop below configured, the budget the gateway sets without
// the header. A deadline the caller chose then never counts against the backend's
// breaker.
func callerBudget(ctx context.Context, hop, configured time.Duration) context.Context {
	if hop < configured {
		return config.WithCallerBudget(ctx)
	}
	return ctx
}

// hopBudget returns the outbound budget for one request: hop, or fallback when the
// timeouts block leaves it at zero. Every hop below then has a deadline, and the
// clients fail closed without one.
func hopBudget(hop, fallback time.Duration) time.Duration {
	if hop > 0 {
		return hop
	}
	return fallback
}
