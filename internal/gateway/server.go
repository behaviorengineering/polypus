package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/batch/service"
	"github.com/behaviorengineering/polypus/internal/clients/anthropic"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/gemini"
	"github.com/behaviorengineering/polypus/internal/clients/openaicompat"
	"github.com/behaviorengineering/polypus/internal/clients/openrouter"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
	"go.opentelemetry.io/otel/attribute"
)

// shared holds dependencies used by capability handlers.
type shared struct {
	routerParts
	opts          config.ServeOptions
	proxy         http.Handler
	invCache      *modelsInventoryCache
	timeouts      config.Timeouts
	transport     proxyTransport
	upstreams     *upstream.Board
	cfGet         cloudflareClientGet
	geminiGet     geminiClientGet
	anthropicGet  anthropicClientGet
	openRouterGet func(config.BackendDef) (*openrouter.Client, error)
	batchStore    *batch.DiskStore
	batches       *service.Service
	claudeBatches service.ClaudeBatches
	// clock is the gateway's job clock: batch stamps, cache entries, dump names.
	clock       func() time.Time
	overlayPath string
	adminKeys   *keys.Store
	uiMounts    []uiProxyMount
	log         *slog.Logger
	compat      openaicompat.Client
	models      config.ModelsInventory
	// dumpDir receives failure dumps the debug route serves; empty turns them off.
	dumpDir string
	// traceSkipPaths are HTTP paths that get no SERVER span.
	traceSkipPaths []string
}

// gatewayHandler is the Polypus HTTP mux (controller) over capability handlers.
type gatewayHandler struct {
	*shared
}

type (
	chatHandler   struct{ *shared }
	modelsHandler struct{ *shared }
	healthHandler struct{ *shared }
	speechHandler struct{ *shared }
)

func (s *shared) cloudflareClient(b config.BackendDef) (*cloudflare.Client, error) {
	return s.cfGet(b)
}

func (s *shared) geminiClient(b config.BackendDef) (*gemini.Client, error) {
	return s.geminiGet(b)
}

func (s *shared) anthropicClient(b config.BackendDef) (*anthropic.Client, error) {
	return s.anthropicGet(b)
}

// chunkDumps is where this gateway's streams dump the chunks they drop.
func (s *shared) chunkDumps() chunkDumpSink {
	return chunkDumpSink{dir: s.dumpDir, log: s.log, now: s.clock}
}

// NewHandler returns the public Polypus HTTP handler (*Gateway).
// It does not write Switchyard TOML; ListenAndServe (process startup) does.
// Tests pass withRouter to inject a fake and skip bifrost.Init.
func NewHandler(opts config.ServeOptions, options ...HandlerOption) (http.Handler, error) {
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
	parts, rcfg, err := openRouter(opts, ho)
	if err != nil {
		return nil, err
	}
	s, err := sharedConfig{
		serve:     opts,
		options:   ho,
		router:    parts,
		routerCfg: rcfg,
		adminKeys: adminKeys,
	}.createShared()
	if err != nil {
		if parts.closeRouter != nil {
			parts.closeRouter()
		}
		return nil, err
	}
	return wrapGatewayAccess(adminKeys, &gatewayHandler{shared: s}), nil
}

// openRouter returns the injected router, or loads the router config, applies the
// allow overlay and builds a Bifrost client that the gateway then owns.
func openRouter(opts config.ServeOptions, ho handlerOptions) (routerParts, config.RouterConfig, error) {
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
	clientOpts := []router.ClientOption{router.WithLogger(ho.logger)}
	if ho.cfGet != nil {
		clientOpts = append(clientOpts, router.WithCloudflareClientGet(router.CloudflareClientGet(ho.cfGet)))
	}
	client, err := router.NewClient(loaded, clientOpts...)
	if err != nil {
		return routerParts{}, config.RouterConfig{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "bifrost client")
	}
	parts := splitRouter(client)
	parts.closeRouter = client.Close
	return parts, loaded, nil
}

// sharedConfig is everything createShared needs to build one gateway's state.
type sharedConfig struct {
	serve     config.ServeOptions
	options   handlerOptions
	router    routerParts
	routerCfg config.RouterConfig
	adminKeys *keys.Store
}

// createShared builds the per-gateway state: fallback proxy, timeouts, batch store
// and UI mounts. The caller closes an owned router when it fails. options must have
// been through fillDefaults.
func (c sharedConfig) createShared() (*shared, error) {
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
	models, err := config.LoadModelsInventory()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "models inventory")
	}
	sh := &shared{
		routerParts:    c.router,
		models:         models,
		opts:           c.serve,
		invCache:       newModelsInventoryCache(ho.logger, models.CacheTTL, ho.clock),
		timeouts:       timeouts,
		transport:      newProxyTransport(timeouts.Max, ho.policies),
		upstreams:      upstream.NewBoard(),
		cfGet:          ho.cfGet,
		geminiGet:      ho.geminiGet,
		anthropicGet:   ho.anthropicGet,
		openRouterGet:  ho.openRouterGet,
		batchStore:     batchStore,
		clock:          ho.clock,
		overlayPath:    ho.overlayPath,
		adminKeys:      c.adminKeys,
		log:            ho.logger,
		dumpDir:        ho.dumpDir,
		traceSkipPaths: ho.traceSkipPaths,
		compat:         openaicompat.Client{HTTP: tracedProbeClient, Policies: ho.policies},
	}
	if sh.proxy, err = sh.newFallbackProxy(sh.registry.ProxyBackendURL()); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "fallback proxy")
	}
	if sh.uiMounts, err = sh.buildUIMounts(c.routerCfg.UIProxies); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "ui proxies")
	}
	sh.claudeBatches = ho.claudeBatches
	if c.router.bifrost != nil && sh.claudeBatches == nil {
		sh.claudeBatches = router.NewClaudeBatches(c.router.bifrost)
	}
	sh.batches, err = service.Config{
		Store:     batchStore,
		Breakers:  sh.upstreams,
		Now:       ho.clock,
		Submitter: sh.batchSubmitter,
	}.CreateService()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), op, "batch service")
	}
	return sh, nil
}

// batchSubmitter picks the batch submitter for a backend.
func (s *shared) batchSubmitter(b config.BackendDef) (service.Submitter, error) {
	switch {
	case b.IsAnthropicExtension():
		if s.claudeBatches == nil {
			return nil, derrors.New(derrors.CodeNotReady, "gateway.batchSubmitter", "claude batches need the bifrost router").
				With("backend", b.ID)
		}
		return service.NewClaudeSubmitter(s.claudeBatches, b), nil
	case b.IsOpenRouter():
		return service.NewOpenRouterSubmitter(func() (*openrouter.Client, error) { return s.openRouterGet(b) }), nil
	case b.IsCloudflareExtension():
		return service.NewCloudflareSubmitter(func() (*cloudflare.Client, error) { return s.cloudflareClient(b) }, s.clock), nil
	default:
		return nil, derrors.New(derrors.CodeFailedPrecondition, "gateway.batchSubmitter",
			"batch only supported for cloudflare, openrouter and anthropic backends").With("backend", b.ID)
	}
}

func (s *shared) newFallbackProxy(backendURL string) (http.Handler, error) {
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
			s.writeErrorJSON(w, http.StatusBadGateway,
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
		healthHandler{g.shared}.serveHealth(w, r)
	}},
	{path: pathHealthBackends, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		healthHandler{g.shared}.serveBackendHealth(w, r)
	}},
	{path: pathHealthUpstreams, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		healthHandler{g.shared}.serveUpstreamHealth(w, r)
	}},
	{path: pathDebugFailures, prefix: true, methods: methodsGet, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		healthHandler{g.shared}.serveFailureDump(w, r)
	}},
	{path: pathAPIs, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		apiCatalogHandler{g.shared}.serveCatalog(w, r)
	}},
	{path: pathOpenAISpec, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) { g.serveOpenAPISpec(w, r) }},
	{path: pathSystemOneSchema, methods: methodsRead, serve: (*gatewayHandler).serveSystemOneSchemaRoute},
	{path: pathOpenAIModels, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelsListForSurface(surfaceOpenAI, w, r)
	}},
	{path: pathOpenAIModels + "/", prefix: true, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelRetrieveForSurface(surfaceOpenAI, pathOpenAIModels+"/", w, r)
	}},
	{path: pathSystemOneModels, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelsListForSurface(surfaceSystemOne, w, r)
	}},
	{path: pathSystemOneModels + "/", prefix: true, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelRetrieveForSurface(surfaceSystemOne, pathSystemOneModels+"/", w, r)
	}},
	{path: pathModels, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelsList(w, r)
	}},
	{path: pathModelsPrefix, prefix: true, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelRetrieve(w, r)
	}},
	{path: pathChatCompletions, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		chatHandler{g.shared}.serveChatCompletions(w, r)
	}},
	{path: pathEmbeddings, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		chatHandler{g.shared}.serveEmbeddings(w, r)
	}},
	{path: pathAudioSpeech, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		speechHandler{g.shared}.serveSpeech(w, r)
	}},
	{path: pathAudioTranscriptions, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		speechHandler{g.shared}.serveTranscription(w, r)
	}},
	{path: pathAudioVoices, methods: methodsRead, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) { g.proxy.ServeHTTP(w, r) }},
	{path: pathSystemOne, methods: methodsPost, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		systemOneHandler{g.shared}.serveSystemOne(w, r)
	}},
	{path: pathFiles, prefix: true, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		filesHandler{g.shared}.serveFiles(w, r)
	}},
	{path: pathBatches, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		batchesHandler{g.shared}.serveBatches(w, r)
	}},
	{path: pathBatchesPrefix, prefix: true, methods: methodsAny, serve: func(g *gatewayHandler, w http.ResponseWriter, r *http.Request) {
		batchesHandler{g.shared}.serveBatchByID(w, r)
	}},
	{path: pathAdminModelsAllow, methods: methodsAny, serve: (*gatewayHandler).serveModelsAllowRoute},
}

func (g *gatewayHandler) serveSystemOneSchemaRoute(w http.ResponseWriter, r *http.Request) {
	cfg := config.RouterConfig{}
	if g.registry != nil {
		cfg = g.registry.Config()
	}
	if !systemOneAPIEnabled(cfg) {
		g.writeAPINotConfigured(w)
		return
	}
	g.serveSystemOneSchema(w, r)
}

func (g *gatewayHandler) serveModelsAllowRoute(w http.ResponseWriter, r *http.Request) {
	h := adminHandler{g.shared}
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
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveChatCompletions", "read body"))
		return
	}
	req, err := parseChatRequest(body)
	if err != nil {
		h.writeHandlerError(w, err)
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
			h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveChatCompletions", "no chat backend configured"))
			return
		}
		backendID, downstream, err = reg.ResolveChat(model)
	}
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveChatCompletions", "resolve backend"))
		return
	}
	if !h.ensureModelAllowed(backendID, model) {
		h.writeModelNotAllowed(w, model)
		return
	}
	t, err := h.resolveTarget("gateway.serveChatCompletions", backendID, downstream)
	if err != nil {
		h.writeHandlerError(w, err)
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
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unknown router").
			With(keyModel, model).
			With("router", routerName))
		return
	}
	if msg := validateNamedRouterForChat(nr, model, req.vision); msg != "" {
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", msg))
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
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unsupported route type").
			With(keyModel, model).
			With("router", routerName))
	}
}

func (h chatHandler) servePassthroughRouterChat(w http.ResponseWriter, r *http.Request, req chatRequest, target string) {
	backendID, downstream, err := h.registry.ResolveChat(target)
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.servePassthroughRouterChat", "resolve target"))
		return
	}
	if !h.ensureModelAllowed(backendID, target) {
		h.writeModelNotAllowed(w, target)
		return
	}
	t, err := h.resolveTarget("gateway.servePassthroughRouterChat", backendID, downstream)
	if err != nil {
		h.writeHandlerError(w, err)
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
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)
	hop := hopBudget(h.timeouts.Max, config.DefaultTimeouts().Max)
	err = h.upstreams.Execute(upstream.NameSwitchyard, func() error {
		return proxyChat(w, r, req.raw, chatDial{
			backendURL:                switchyardURL,
			transport:                 h.transport,
			hop:                       hop,
			streamIdle:                h.timeouts.ResolveStreamIdle(upstream.NameSwitchyard),
			stream:                    req.stream,
			recordRouterSelectedModel: true,
			log:                       h.log,
			chunkDumps:                h.chunkDumps(),
		})
	})
	if err != nil {
		h.writeUpstreamDialError(w, dialFailure{
			err:         err,
			prefix:      "polypus: switchyard unavailable: ",
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
func (s *shared) writeHandlerError(w http.ResponseWriter, err error) {
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	if s.writeRateLimitError(w, err) {
		return
	}
	if upstream.Unavailable(err) {
		name := upstream.ResolveUpstreamName(err, "")
		s.log.Warn("polypus: upstream unavailable", "upstream", name, "err", err)
		s.writeUnavailableError(w, err, derrors.PublicMessage(err), name)
		return
	}
	status := derrors.HTTPStatus(err)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	s.writeErrorJSON(w, status, err, "")
}

// writeErrorJSON answers with an OpenAI-shaped error body. The client sees the
// public message; the full chain (ops, upstream URLs, causes) goes to the log.
func (s *shared) writeErrorJSON(w http.ResponseWriter, status int, err error, prefix string) {
	if status >= http.StatusInternalServerError {
		s.log.Warn("polypus: request failed", "status", status, "err", err)
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
	s.writeJSON(w, status, body)
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
func (s *shared) writeUpstreamDialError(w http.ResponseWriter, f dialFailure) {
	err, prefix, upstreamName, unreachable := f.err, f.prefix, f.upstream, f.unreachable
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	if s.writeRateLimitError(w, err) {
		return
	}
	if upstream.Unavailable(err) || (unreachable != nil && unreachable(err)) {
		s.log.Warn("polypus: upstream unavailable", "upstream", upstreamName, "err", err)
		s.writeUnavailableError(w, err, prefix+derrors.PublicMessage(err), upstreamName)
		return
	}
	code := http.StatusBadGateway
	var de *derrors.Error
	if errors.As(err, &de) {
		code = derrors.HTTPStatus(err)
	}
	s.writeErrorJSON(w, code, err, prefix)
}

func (h chatHandler) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	var dialUpstream string
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, embedMaxBody))
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveEmbeddings", "read body"))
		return
	}
	model, err := bodyModel("gateway.serveEmbeddings", body, true)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.embeddings", observability.LLMSpanAttrs{
		Model: model,
	})
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)

	reg := h.registry
	cfg := reg.Config()
	if cfg.EffectiveEmbedBackend() == "" {
		err = derrors.New(derrors.CodeNotReady, "gateway.serveEmbeddings", "no embed backend configured").
			With(derrors.FieldIntervention, string(observability.InterventionEmbedCapabilityMissing))
		recordInterventionFromError(ctx, err)
		h.writeHandlerError(w, err)
		return
	}
	backendID, downstream, err := reg.ResolveEmbed(model)
	if err != nil {
		recordInterventionFromError(ctx, err)
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveEmbeddings", "resolve backend"))
		return
	}
	if !h.ensureModelAllowed(backendID, model) {
		h.writeModelNotAllowed(w, model)
		return
	}
	t, err := h.resolveTarget("gateway.serveEmbeddings", backendID, downstream)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	backendURL := t.url
	span.SetAttributes(
		attribute.String("polypus.backend_id", backendID),
		attribute.String("polypus.backend_url", backendURL),
		attribute.String("polypus.downstream_model", downstream),
	)
	rewritten, err := withBodyModel("gateway.serveEmbeddings", body, downstream)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	dialUpstream = backendID
	hop := h.timeouts.ResolveEmbed(r.Header.Get(config.TimeoutHeader))
	err = h.upstreams.Execute(backendID, func() error {
		if h.chat.UsesBifrost(backendID) {
			raw, bifrostErr := h.chat.EmbeddingRaw(r.Context(), router.Call{BackendID: backendID, Model: downstream, Body: rewritten, Timeout: hop})
			if bifrostErr != nil {
				return bifrostErr
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, writeErr := w.Write(raw)
			if writeErr != nil {
				return afterHeaders(r.Context(), writeErr, true, "gateway.serveEmbeddings", "write response")
			}
			return nil
		}
		return h.proxyEmbeddings(w, r, embedCall{target: t, body: rewritten, hop: hop})
	})
	if err != nil {
		h.writeUpstreamDialError(w, dialFailure{err: err, upstream: string(backendID)})
		return
	}
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
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSpeech", "read body"))
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
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSpeech", "invalid json"))
		return
	}
	backendID, downstream, resolveErr := h.registry.ResolveTTS(req.Model)
	if resolveErr != nil {
		h.writeHandlerError(w, derrors.Wrap(resolveErr, derrors.CodeInvalid, "gateway.serveSpeech", "resolve backend").
			With(keyModel, req.Model))
		return
	}
	if strings.TrimSpace(req.Model) != "" && !h.ensureModelAllowed(string(backendID), req.Model) {
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
	if err := r.ParseMultipartForm(transcriptionMaxMemory); err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "multipart"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "file required"))
		return
	}
	defer func() { _ = file.Close() }()
	audio, err := io.ReadAll(file)
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "read file"))
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
		h.writeHandlerError(w, derrors.Wrap(resolveErr, derrors.CodeInvalid, "gateway.serveTranscription", "resolve backend").
			With(keyModel, sttModel))
		return
	}
	if strings.TrimSpace(sttModel) != "" && !h.ensureModelAllowed(string(backendID), sttModel) {
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
	defer func() { observability.EndDialSpan(span, err, hop.backendID) }()
	budget := hopBudget(h.timeouts.ResolveSpeech(r.Header.Get(config.TimeoutHeader)), config.DefaultTimeouts().Speech)
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var body []byte
	var contentType string
	err = h.upstreams.Execute(hop.backendID, func() error {
		var callErr error
		body, contentType, callErr = call(ctx)
		return callErr
	})
	if err != nil {
		h.writeUpstreamDialError(w, dialFailure{err: err, upstream: hop.backendID})
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

// hopBudget returns the outbound budget for one request: hop, or fallback when the
// timeouts block leaves it at zero. Every hop below then has a deadline, and the
// clients fail closed without one.
func hopBudget(hop, fallback time.Duration) time.Duration {
	if hop > 0 {
		return hop
	}
	return fallback
}
