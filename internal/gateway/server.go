package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/anthropic"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/clients/gemini"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// shared holds dependencies used by capability handlers.
type shared struct {
	opts         config.ServeOptions
	router       Router
	ownedRouter  bool
	proxy        http.Handler
	invCache     *modelsInventoryCache
	timeouts     config.Timeouts
	client       *http.Client
	upstreams    *upstream.Board
	cfGet        CloudflareClientGet
	geminiGet    GeminiClientGet
	anthropicGet AnthropicClientGet
	batchStore   *batch.DiskStore
	batchNow     func() time.Time
	overlayPath  string
	adminKeys    *keys.Store
	uiMounts     []uiProxyMount
}

// Gateway is the Polypus HTTP mux (controller) over capability handlers.
type Gateway struct {
	*shared
}

type (
	chatHandler   struct{ *shared }
	modelsHandler struct{ *shared }
	healthHandler struct{ *shared }
	speechHandler struct{ *shared }
)

func (s *shared) cloudflareClient(b config.BackendDef) (*cloudflare.Client, error) {
	if s != nil && s.cfGet != nil {
		return s.cfGet(b)
	}
	return cloudflare.GetClient(b)
}

func (s *shared) geminiClient(b config.BackendDef) (*gemini.Client, error) {
	if s != nil && s.geminiGet != nil {
		return s.geminiGet(b)
	}
	return gemini.GetClient(b)
}

func (s *shared) anthropicClient(b config.BackendDef) (*anthropic.Client, error) {
	if s != nil && s.anthropicGet != nil {
		return s.anthropicGet(b)
	}
	return anthropic.GetClient(b)
}

// NewHandler returns the public Polypus HTTP handler (*Gateway).
// It does not write Switchyard TOML; ListenAndServe (process startup) does.
// Pass WithRouter to inject a fake and skip bifrost.Init (tests).
func NewHandler(opts config.ServeOptions, options ...HandlerOption) (http.Handler, error) {
	var ho handlerOptions
	for _, opt := range options {
		if opt != nil {
			opt(&ho)
		}
	}
	if err := ho.fillDefaults(); err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	adminKeys, err := keys.Config{Path: ho.adminKeysPath, Clock: ho.clock}.CreateStore()
	if err != nil {
		return nil, fmt.Errorf("gateway: admin keys: %w", err)
	}
	rc, rcfg, owned, err := openRouter(opts, ho)
	if err != nil {
		return nil, err
	}
	s, err := newShared(opts, ho, rc, rcfg, adminKeys)
	if err != nil {
		if c, ok := rc.(routerCloser); ok && owned {
			c.Close()
		}
		return nil, err
	}
	s.ownedRouter = owned
	return wrapGatewayAccess(adminKeys, &Gateway{shared: s}), nil
}

// openRouter returns the injected router, or loads the router config, applies the
// allow overlay and builds a Bifrost client that the gateway then owns.
func openRouter(opts config.ServeOptions, ho handlerOptions) (rc Router, rcfg config.RouterConfig, owned bool, err error) {
	if ho.router != nil {
		return ho.router, ho.router.Registry().Config(), false, nil
	}
	loaded, err := config.LoadRouterConfig(opts)
	if err != nil {
		return nil, config.RouterConfig{}, false, fmt.Errorf("gateway: %w", err)
	}
	if err := config.ApplyAllowOverlay(&loaded, ho.overlayPath); err != nil {
		return nil, config.RouterConfig{}, false, fmt.Errorf("gateway: allow overlay: %w", err)
	}
	var clientOpts []router.ClientOption
	if ho.cfGet != nil {
		clientOpts = append(clientOpts, router.WithCloudflareClientGet(router.CloudflareClientGet(ho.cfGet)))
	}
	client, err := router.NewClient(loaded, clientOpts...)
	if err != nil {
		return nil, config.RouterConfig{}, false, fmt.Errorf("gateway: %w", err)
	}
	return client, loaded, true, nil
}

// newShared builds the per-gateway state: fallback proxy, timeouts, batch store
// and UI mounts. The caller closes an owned router when it fails.
func newShared(opts config.ServeOptions, ho handlerOptions, rc Router, rcfg config.RouterConfig, adminKeys *keys.Store) (*shared, error) {
	proxy, err := newFallbackProxy(rc.Registry().ProxyBackendURL())
	if err != nil {
		return nil, err
	}
	timeouts := rcfg.Timeouts
	if timeouts.Max == 0 {
		timeouts = config.DefaultTimeouts()
	}
	batchRoot, err := config.ResolveBatchDir()
	if err != nil {
		return nil, fmt.Errorf("gateway: batch store: %w", err)
	}
	batchStore, err := batch.NewDiskStore(batchRoot, ho.clock)
	if err != nil {
		return nil, fmt.Errorf("gateway: batch store: %w", err)
	}
	uiMounts, err := buildUIMounts(rcfg.UIProxies)
	if err != nil {
		return nil, err
	}
	return &shared{
		opts:         opts,
		router:       rc,
		proxy:        proxy,
		invCache:     newModelsInventoryCache(),
		timeouts:     timeouts,
		client:       newChatProxyClient(timeouts.Max),
		upstreams:    upstream.NewBoard(),
		cfGet:        ho.cfGet,
		geminiGet:    ho.geminiGet,
		anthropicGet: ho.anthropicGet,
		batchStore:   batchStore,
		batchNow:     ho.clock,
		overlayPath:  ho.overlayPath,
		adminKeys:    adminKeys,
		uiMounts:     uiMounts,
	}, nil
}

func (s *shared) batchNowTime() time.Time {
	if s != nil && s.batchNow != nil {
		return s.batchNow()
	}
	return time.Now().UTC()
}

func newFallbackProxy(backendURL string) (http.Handler, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, fmt.Errorf("backend url: %w", err)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			writeErrorJSON(w, http.StatusBadGateway,
				derrors.Wrap(err, derrors.CodeUnavailable, "gateway.backendProxy", "polypus backend unavailable"), "")
		},
	}
	return proxy, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// Which methods a route answers.
const (
	methodsRead = iota // GET and HEAD
	methodsGet         // GET only
	methodsPost        // POST only
	methodsAny         // the handler checks the method itself
)

// gatewayRoute is one entry of the routing table. Routes are tried in order, so a
// prefix route must come after the exact routes it would otherwise shadow.
type gatewayRoute struct {
	path    string
	prefix  bool
	methods int
	serve   func(g *Gateway, w http.ResponseWriter, r *http.Request)
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
	default:
		return true
	}
}

var gatewayRoutes = []gatewayRoute{
	{path: "/", methods: methodsAny, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { g.serveLanding(w, r) }},
	{path: bannerAssetPath, methods: methodsAny, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { g.serveBannerWebP(w, r) }},
	{path: "/health", methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { healthHandler{g.shared}.serveHealth(w, r) }},
	{path: "/health/backends", methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		healthHandler{g.shared}.serveBackendHealth(w, r)
	}},
	{path: "/health/upstreams", methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		healthHandler{g.shared}.serveUpstreamHealth(w, r)
	}},
	{path: "/debug/failures/", prefix: true, methods: methodsGet, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		healthHandler{g.shared}.serveFailureDump(w, r)
	}},
	{path: "/v1/apis", methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		apiCatalogHandler{g.shared}.serveCatalog(w, r)
	}},
	{path: "/v1/apis/openai/openapi.yaml", methods: methodsRead, serve: func(_ *Gateway, w http.ResponseWriter, r *http.Request) { serveOpenAPISpec(w, r) }},
	{path: "/v1/apis/systemone/schema.json", methods: methodsRead, serve: (*Gateway).serveSystemOneSchemaRoute},
	{path: pathOpenAIModels, methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelsListForSurface(surfaceOpenAI, w, r)
	}},
	{path: pathOpenAIModels + "/", prefix: true, methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelRetrieveForSurface(surfaceOpenAI, pathOpenAIModels+"/", w, r)
	}},
	{path: pathSystemOneModels, methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelsListForSurface(surfaceSystemOne, w, r)
	}},
	{path: pathSystemOneModels + "/", prefix: true, methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelRetrieveForSurface(surfaceSystemOne, pathSystemOneModels+"/", w, r)
	}},
	{path: "/v1/models", methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelsList(w, r)
	}},
	{path: "/v1/models/", prefix: true, methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		modelsHandler{g.shared}.serveModelRetrieve(w, r)
	}},
	{path: "/v1/chat/completions", methods: methodsPost, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		chatHandler{g.shared}.serveChatCompletions(w, r)
	}},
	{path: "/v1/embeddings", methods: methodsPost, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { chatHandler{g.shared}.serveEmbeddings(w, r) }},
	{path: "/v1/audio/speech", methods: methodsPost, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { speechHandler{g.shared}.serveSpeech(w, r) }},
	{path: "/v1/audio/transcriptions", methods: methodsPost, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		speechHandler{g.shared}.serveTranscription(w, r)
	}},
	{path: "/v1/audio/voices", methods: methodsRead, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { g.proxy.ServeHTTP(w, r) }},
	{path: "/v1/systemone", methods: methodsPost, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		systemOneHandler{g.shared}.serveSystemOne(w, r)
	}},
	{path: "/v1/files", prefix: true, methods: methodsAny, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { filesHandler{g.shared}.serveFiles(w, r) }},
	{path: "/v1/batches", methods: methodsAny, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) { batchesHandler{g.shared}.serveBatches(w, r) }},
	{path: "/v1/batches/", prefix: true, methods: methodsAny, serve: func(g *Gateway, w http.ResponseWriter, r *http.Request) {
		batchesHandler{g.shared}.serveBatchByID(w, r)
	}},
	{path: "/v1/admin/models/allow", methods: methodsAny, serve: (*Gateway).serveModelsAllowRoute},
}

func (g *Gateway) serveSystemOneSchemaRoute(w http.ResponseWriter, r *http.Request) {
	cfg := config.RouterConfig{}
	if g.router != nil {
		cfg = g.router.Registry().Config()
	}
	if !systemOneAPIEnabled(cfg) {
		writeAPINotConfigured(w)
		return
	}
	serveSystemOneSchema(w, r)
}

func (g *Gateway) serveModelsAllowRoute(w http.ResponseWriter, r *http.Request) {
	h := adminHandler{g.shared}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.serveModelsAllowOverlay(w, r)
	case http.MethodPost:
		h.serveModelsAllow(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h chatHandler) serveChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, chatMaxBody))
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveChatCompletions", "read body"))
		return
	}
	model, err := extractChatModel(body)
	if err != nil {
		writeHandlerError(w, err)
		return
	}

	reg := h.router.Registry()
	cfg := reg.Config()
	vision := chatBodyHasVision(body)

	if routerName, ok := parseNamedRouterModel(model); ok {
		h.serveNamedRouterChat(w, r, body, model, routerName, vision)
		return
	}

	var backendID, downstream string
	if vision {
		backendID, downstream, err = reg.ResolveVision(model)
	} else {
		if cfg.EffectiveChatBackend() == "" {
			writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveChatCompletions", "no chat backend configured"))
			return
		}
		backendID, downstream, err = reg.ResolveChat(model)
	}
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveChatCompletions", "resolve backend"))
		return
	}
	if !h.ensureModelAllowed(backendID, model) {
		writeModelNotAllowed(w, model)
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveChatCompletions", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveChatCompletions", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backendAuth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		writeHandlerError(w, authErr)
		return
	}
	rewritten, err := rewriteChatModel(body, downstream)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.chat", model, backendID, backendURL, downstream)
	dialUpstream := backendID
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, vision, chatBodyWantsThinking(body))
	if err = h.proxyOrBifrostChat(w, r, backendID, downstream, backendURL, rewritten, hop, backendAuth, true); err != nil {
		writeUpstreamDialError(w, err, "", string(backendID), nil)
		return
	}
}

func (h chatHandler) serveNamedRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, routerName string, vision bool) {
	reg := h.router.Registry()
	cfg := reg.Config()
	nr, ok := lookupRouter(cfg, routerName)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unknown router").
			With(keyModel, model).
			With("router", routerName))
		return
	}
	if msg := validateNamedRouterForChat(nr, model, vision); msg != "" {
		writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", msg))
		return
	}

	switch classifyNamedRouterRoute(nr.Route.Type) {
	case dispatchPassthrough:
		h.servePassthroughRouterChat(w, r, body, model, nr.Route.Target)
	case dispatchSwitchyard:
		h.serveSwitchyardRouterChat(w, r, body, model, routerName, cfg.EffectiveSwitchyardBaseURL())
	case dispatchUnknown:
		fallthrough
	default:
		writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unsupported route type").
			With(keyModel, model).
			With("router", routerName))
	}
}

func (h chatHandler) servePassthroughRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, target string) {
	reg := h.router.Registry()
	backendID, downstream, resolveErr := reg.ResolveChat(target)
	if resolveErr != nil {
		writeHandlerError(w, derrors.Wrap(resolveErr, derrors.CodeInvalid, "gateway.servePassthroughRouterChat", "resolve target"))
		return
	}
	if !h.ensureModelAllowed(backendID, target) {
		writeModelNotAllowed(w, target)
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.servePassthroughRouterChat", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.servePassthroughRouterChat", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backendAuth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		writeHandlerError(w, authErr)
		return
	}
	rewritten, rewriteErr := rewriteChatModel(body, downstream)
	if rewriteErr != nil {
		writeHandlerError(w, rewriteErr)
		return
	}
	var err error
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.chat", model, backendID, backendURL, downstream)
	dialUpstream := backendID
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, false, chatBodyWantsThinking(body))
	if err = h.proxyOrBifrostChat(w, r, backendID, downstream, backendURL, rewritten, hop, backendAuth, true); err != nil {
		writeUpstreamDialError(w, err, "", string(backendID), nil)
	}
}

func (h chatHandler) serveSwitchyardRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, routerName, switchyardURL string) {
	var err error
	ctx, span := observability.StartRouterSpan(r.Context(), "polypus.router", model, routerName, switchyardURL)
	dialUpstream := upstream.NameSwitchyard
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.Max
	if hop <= 0 {
		hop = config.DefaultTimeouts().Max
	}
	err = h.upstreams.Execute(upstream.NameSwitchyard, func() error {
		return proxyChat(w, r, body, chatDial{
			backendURL:                switchyardURL,
			client:                    h.client,
			hop:                       hop,
			streamIdle:                h.timeouts.ResolveStreamIdle(upstream.NameSwitchyard),
			recordRouterSelectedModel: true,
		})
	})
	if err != nil {
		writeUpstreamDialError(w, err, "polypus: switchyard unavailable: ", upstream.NameSwitchyard, isSwitchyardUnreachable)
	}
}

func probeSwitchyard(ctx context.Context, baseURL string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	ctx, cancel := outbound.WithDeadlineIfMissing(ctx, backendProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: backendProbeTimeout}
	resp, err := outbound.Do(ctx, outbound.DepHealth, client, req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func isSwitchyardUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connect: connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection reset")
}

// writeHandlerError writes a domain error (or any error) using HTTPStatus.
func writeHandlerError(w http.ResponseWriter, err error) {
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	if writeRateLimitError(w, err) {
		return
	}
	if upstream.Unavailable(err) {
		writeUnavailableError(w, err, err.Error(), upstream.ResolveUpstreamName(err, ""))
		return
	}
	status := derrors.HTTPStatus(err)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		status = http.StatusRequestEntityTooLarge
	}
	writeErrorJSON(w, status, err, "")
}

// writeErrorJSON answers with an OpenAI-shaped error body. The client sees the
// public message; the full chain (ops, upstream URLs, causes) goes to the log.
func writeErrorJSON(w http.ResponseWriter, status int, err error, prefix string) {
	if status >= http.StatusInternalServerError {
		slog.Warn("polypus: request failed", "status", status, "err", err)
	}
	writeOpenAIJSON(w, status, openaiErrorBody{Error: openaiError{
		Message: prefix + derrors.PublicMessage(err),
		Type:    openaiErrorType(status),
		Code:    string(derrors.CodeOf(err)),
	}})
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

// writeUpstreamDialError writes a dial failure unless the upstream body was already sent.
// prefix is prepended for Switchyard-style messages; unreachable maps to 503 when set.
// Typed domain errors use HTTPStatus; other errors stay 502 unless the breaker or unreachable hook says 503.
func writeUpstreamDialError(w http.ResponseWriter, err error, prefix, upstreamName string, unreachable func(error) bool) {
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	if writeRateLimitError(w, err) {
		return
	}
	if upstream.Unavailable(err) || (unreachable != nil && unreachable(err)) {
		slog.Warn("polypus: upstream unavailable", "upstream", upstreamName, "err", err)
		writeUnavailableError(w, err, prefix+derrors.PublicMessage(err), upstreamName)
		return
	}
	code := http.StatusBadGateway
	var de *derrors.Error
	if errors.As(err, &de) {
		code = derrors.HTTPStatus(err)
	}
	writeErrorJSON(w, code, err, prefix)
}

func (h chatHandler) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, embedMaxBody))
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveEmbeddings", "read body"))
		return
	}
	model, err := extractEmbedModel(body)
	if err != nil {
		writeHandlerError(w, err)
		return
	}

	reg := h.router.Registry()
	cfg := reg.Config()
	if cfg.EffectiveEmbedBackend() == "" {
		writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveEmbeddings", "no embed backend configured"))
		return
	}
	backendID, downstream, err := reg.ResolveEmbed(model)
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveEmbeddings", "resolve backend"))
		return
	}
	if !h.ensureModelAllowed(backendID, model) {
		writeModelNotAllowed(w, model)
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveEmbeddings", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveEmbeddings", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backendAuth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		writeHandlerError(w, authErr)
		return
	}
	rewritten, err := rewriteEmbedModel(body, downstream)
	if err != nil {
		writeHandlerError(w, err)
		return
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.embeddings", model, backendID, backendURL, downstream)
	dialUpstream := backendID
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.ResolveEmbed(r.Header.Get(config.TimeoutHeader))
	err = h.upstreams.Execute(backendID, func() error {
		if h.router.UsesBifrost(backendID) {
			raw, bifrostErr := h.router.EmbeddingRaw(r.Context(), backendID, downstream, rewritten, hop)
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
		return proxyEmbeddings(w, r, backendURL, rewritten, h.client, hop, backendAuth)
	})
	if err != nil {
		writeUpstreamDialError(w, err, "", string(backendID), nil)
		return
	}
}

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
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSpeech", "read body"))
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
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSpeech", "invalid json"))
		return
	}
	backendID, downstream, resolveErr := h.router.Registry().ResolveTTS(req.Model)
	if resolveErr != nil {
		writeHandlerError(w, derrors.Wrap(resolveErr, derrors.CodeInvalid, "gateway.serveSpeech", "resolve backend").
			With(keyModel, req.Model))
		return
	}
	if strings.TrimSpace(req.Model) != "" && !h.ensureModelAllowed(string(backendID), req.Model) {
		writeModelNotAllowed(w, req.Model)
		return
	}
	backendURL, _ := h.router.Registry().BackendURL(string(backendID))
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.speech", req.Model, string(backendID), backendURL, downstream)
	dialUpstream := string(backendID)
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	hop := h.timeouts.ResolveSpeech(r.Header.Get(config.TimeoutHeader))
	hop = hopBudget(hop, config.DefaultTimeouts().Speech)
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	var audio []byte
	err = h.upstreams.Execute(string(backendID), func() error {
		var synthErr error
		audio, synthErr = h.router.Synthesize(ctx, router.SpeechRequest{
			Model:          req.Model,
			Input:          req.Input,
			Voice:          req.Voice,
			ResponseFormat: req.ResponseFormat,
			Speed:          req.Speed,
		})
		return synthErr
	})
	if err != nil {
		writeUpstreamDialError(w, err, "", string(backendID), nil)
		return
	}
	w.Header().Set("Content-Type", speechContentType(req.ResponseFormat))
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(audio); writeErr != nil {
		err = derrors.Wrap(writeErr, derrors.CodeInternal, "gateway.serveSpeech", "write")
		return
	}
}

func (h speechHandler) serveTranscription(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, transcriptionMaxBody)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "multipart"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "file required"))
		return
	}
	defer func() { _ = file.Close() }()
	audio, err := io.ReadAll(file)
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveTranscription", "read file"))
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
	backendID, downstream, resolveErr := h.router.Registry().ResolveSTT(sttModel)
	if resolveErr != nil {
		writeHandlerError(w, derrors.Wrap(resolveErr, derrors.CodeInvalid, "gateway.serveTranscription", "resolve backend").
			With(keyModel, sttModel))
		return
	}
	if strings.TrimSpace(sttModel) != "" && !h.ensureModelAllowed(string(backendID), sttModel) {
		writeModelNotAllowed(w, sttModel)
		return
	}
	backendURL, _ := h.router.Registry().BackendURL(string(backendID))
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.transcription", sttModel, string(backendID), backendURL, downstream)
	dialUpstream := string(backendID)
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	hop := h.timeouts.ResolveSpeech(r.Header.Get(config.TimeoutHeader))
	hop = hopBudget(hop, config.DefaultTimeouts().Speech)
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	var out []byte
	var ct string
	err = h.upstreams.Execute(string(backendID), func() error {
		var trErr error
		out, ct, trErr = h.router.Transcribe(ctx, router.TranscriptionRequest{
			Model:          sttModel,
			Audio:          audio,
			Filename:       filename,
			ResponseFormat: format,
			Language:       r.FormValue("language"),
		})
		return trErr
	})
	if err != nil {
		writeUpstreamDialError(w, err, "", string(backendID), nil)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(out); writeErr != nil {
		err = derrors.Wrap(writeErr, derrors.CodeInternal, "gateway.serveTranscription", "write")
		return
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
