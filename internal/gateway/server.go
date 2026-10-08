package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// shared holds dependencies used by capability handlers.
type shared struct {
	opts        config.ServeOptions
	router      Router
	ownedRouter bool
	proxy       http.Handler
	invCache    *modelsInventoryCache
	timeouts    config.Timeouts
	client      *http.Client
	upstreams   *upstream.Board
	cfGet       CloudflareClientGet
	batchStore  *batch.DiskStore
	batchNow    func() time.Time
	overlayPath string
	adminKeys   *keys.Store
	uiMounts    []uiProxyMount
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

	overlayPath := ho.overlayPath
	if overlayPath == "" {
		overlayPath = config.ResolveModelsAllowOverlayPath()
	}
	adminKeysPath := ho.adminKeysPath
	if adminKeysPath == "" {
		adminKeysPath = config.ResolveAdminKeysPath()
	}
	jobClock := ho.clock
	if jobClock == nil {
		jobClock = func() time.Time { return time.Now().UTC() }
	}
	adminKeys, err := keys.Config{Path: adminKeysPath, Clock: jobClock}.CreateStore()
	if err != nil {
		return nil, fmt.Errorf("gateway: admin keys: %w", err)
	}

	cfGet := ho.cfGet
	if cfGet == nil {
		cfGet = cloudflare.GetClient
	}

	var rc Router
	owned := false
	var rcfg config.RouterConfig
	if ho.router != nil {
		rc = ho.router
		rcfg = rc.Registry().Config()
	} else {
		loaded, loadErr := config.LoadRouterConfig(opts)
		if loadErr != nil {
			return nil, fmt.Errorf("gateway: %w", loadErr)
		}
		if err := config.ApplyAllowOverlay(&loaded, overlayPath); err != nil {
			return nil, fmt.Errorf("gateway: allow overlay: %w", err)
		}
		rcfg = loaded
		var clientOpts []router.ClientOption
		if ho.cfGet != nil {
			clientOpts = append(clientOpts, router.WithCloudflareClientGet(router.CloudflareClientGet(ho.cfGet)))
		}
		client, clientErr := router.NewClient(rcfg, clientOpts...)
		if clientErr != nil {
			return nil, fmt.Errorf("gateway: %w", clientErr)
		}
		rc = client
		owned = true
	}

	proxyURL := rc.Registry().ProxyBackendURL()
	proxy, err := newFallbackProxy(proxyURL)
	if err != nil {
		if owned {
			if c, ok := rc.(routerCloser); ok {
				c.Close()
			}
		}
		return nil, err
	}
	timeouts := rcfg.Timeouts
	if timeouts.Max == 0 {
		timeouts = config.DefaultTimeouts()
	}
	var batchStore *batch.DiskStore
	if batchRoot := config.ResolveBatchDir(); batchRoot != "" {
		bs, batchStoreErr := batch.NewDiskStore(batchRoot, func() time.Time { return time.Now().UTC() })
		if batchStoreErr != nil {
			if owned {
				if c, ok := rc.(routerCloser); ok {
					c.Close()
				}
			}
			return nil, fmt.Errorf("gateway: batch store: %w", batchStoreErr)
		}
		batchStore = bs
	}
	uiMounts, uiErr := buildUIMounts(rcfg.UIProxies)
	if uiErr != nil {
		if owned {
			if c, ok := rc.(routerCloser); ok {
				c.Close()
			}
		}
		return nil, uiErr
	}
	s := &shared{
		opts:        opts,
		router:      rc,
		ownedRouter: owned,
		proxy:       proxy,
		invCache:    newModelsInventoryCache(),
		timeouts:    timeouts,
		client:      newChatProxyClient(timeouts.Max),
		upstreams:   upstream.NewBoard(),
		cfGet:       cfGet,
		batchStore:  batchStore,
		batchNow:    jobClock,
		overlayPath: overlayPath,
		adminKeys:   adminKeys,
		uiMounts:    uiMounts,
	}
	gw := &Gateway{shared: s}
	return wrapGatewayAccess(adminKeys, gw), nil
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
			http.Error(w, fmt.Sprintf("polypus backend unavailable: %v", err), http.StatusBadGateway)
		},
	}
	return proxy, nil
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/":
		g.serveLanding(w, r)
	case r.URL.Path == bannerAssetPath:
		g.serveBannerWebP(w, r)
	case r.URL.Path == "/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		healthHandler{g.shared}.serveHealth(w, r)
	case r.URL.Path == "/health/backends" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		healthHandler{g.shared}.serveBackendHealth(w, r)
	case r.URL.Path == "/health/upstreams" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		healthHandler{g.shared}.serveUpstreamHealth(w, r)
	case strings.HasPrefix(r.URL.Path, "/debug/failures/") && r.Method == http.MethodGet:
		healthHandler{g.shared}.serveFailureDump(w, r)
	case r.URL.Path == "/v1/apis" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		apiCatalogHandler{g.shared}.serveCatalog(w, r)
	case r.URL.Path == "/v1/apis/openai/openapi.yaml" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		serveOpenAPISpec(w, r)
	case r.URL.Path == "/v1/apis/systemone/schema.json" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		cfg := config.RouterConfig{}
		if g.router != nil {
			cfg = g.router.Registry().Config()
		}
		if !systemOneAPIEnabled(cfg) {
			writeAPINotConfigured(w)
		} else {
			serveSystemOneSchema(w, r)
		}
	case r.URL.Path == "/v1/apis/openai/models" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		modelsHandler{g.shared}.serveModelsListForSurface(surfaceOpenAI, w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/apis/openai/models/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		modelsHandler{g.shared}.serveModelRetrieveForSurface(surfaceOpenAI, "/v1/apis/openai/models/", w, r)
	case r.URL.Path == "/v1/apis/systemone/models" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		modelsHandler{g.shared}.serveModelsListForSurface(surfaceSystemOne, w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/apis/systemone/models/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		modelsHandler{g.shared}.serveModelRetrieveForSurface(surfaceSystemOne, "/v1/apis/systemone/models/", w, r)
	case r.URL.Path == "/v1/models" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		modelsHandler{g.shared}.serveModelsList(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/models/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		modelsHandler{g.shared}.serveModelRetrieve(w, r)
	case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
		chatHandler{g.shared}.serveChatCompletions(w, r)
	case r.URL.Path == "/v1/embeddings" && r.Method == http.MethodPost:
		chatHandler{g.shared}.serveEmbeddings(w, r)
	case r.URL.Path == "/v1/audio/speech" && r.Method == http.MethodPost:
		speechHandler{g.shared}.serveSpeech(w, r)
	case r.URL.Path == "/v1/audio/transcriptions" && r.Method == http.MethodPost:
		speechHandler{g.shared}.serveTranscription(w, r)
	case r.URL.Path == "/v1/audio/voices" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		g.proxy.ServeHTTP(w, r)
	case r.URL.Path == "/v1/systemone" && r.Method == http.MethodPost:
		systemOneHandler{g.shared}.serveSystemOne(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/files"):
		filesHandler{g.shared}.serveFiles(w, r)
	case r.URL.Path == "/v1/batches":
		batchesHandler{g.shared}.serveBatches(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/batches/"):
		batchesHandler{g.shared}.serveBatchByID(w, r)
	case r.URL.Path == "/v1/admin/models/allow" && r.Method == http.MethodPost:
		adminHandler{g.shared}.serveModelsAllow(w, r)
	default:
		if g.serveUIProxy(w, r) {
			return
		}
		http.NotFound(w, r)
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
			With("model", model).
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
	default:
		writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveNamedRouterChat", "unsupported route type").
			With("model", model).
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
		return proxyChatCompletionsOpts(w, r, switchyardURL, body, h.client, hop, "", false, true)
	})
	if err != nil {
		writeUpstreamDialError(w, err, "polypus: switchyard unavailable: ", upstream.NameSwitchyard, isSwitchyardUnreachable)
	}
}

func probeSwitchyard(ctx context.Context, baseURL string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: backendProbeTimeout}
	resp, err := client.Do(req)
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
	http.Error(w, err.Error(), derrors.HTTPStatus(err))
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
	msg := err.Error()
	if prefix != "" {
		msg = prefix + msg
	}
	if upstream.Unavailable(err) || (unreachable != nil && unreachable(err)) {
		writeUnavailableError(w, err, msg, upstreamName)
		return
	}
	code := http.StatusBadGateway
	var de *derrors.Error
	if errors.As(err, &de) {
		code = derrors.HTTPStatus(err)
	}
	http.Error(w, msg, code)
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
				return derrors.Wrap(writeErr, derrors.CodeInternal, "gateway.serveEmbeddings", "write response")
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

func (h speechHandler) serveSpeech(w http.ResponseWriter, r *http.Request) {
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
	if resolveErr == nil && strings.TrimSpace(req.Model) != "" {
		if !h.ensureModelAllowed(string(backendID), req.Model) {
			writeModelNotAllowed(w, req.Model)
			return
		}
	}
	backendURL := ""
	if resolveErr == nil {
		if u, ok := h.router.Registry().BackendURL(string(backendID)); ok {
			backendURL = u
		}
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.speech", req.Model, string(backendID), backendURL, downstream)
	dialUpstream := string(backendID)
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	hop := h.timeouts.ResolveSpeech(r.Header.Get(config.TimeoutHeader))
	if hop > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hop)
		defer cancel()
	}
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
	sttModel := r.FormValue("model")
	backendID, downstream, resolveErr := h.router.Registry().ResolveSTT(sttModel)
	if resolveErr == nil && strings.TrimSpace(sttModel) != "" {
		if !h.ensureModelAllowed(string(backendID), sttModel) {
			writeModelNotAllowed(w, sttModel)
			return
		}
	}
	backendURL := ""
	if resolveErr == nil {
		if u, ok := h.router.Registry().BackendURL(string(backendID)); ok {
			backendURL = u
		}
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.transcription", sttModel, string(backendID), backendURL, downstream)
	dialUpstream := string(backendID)
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	hop := h.timeouts.ResolveSpeech(r.Header.Get(config.TimeoutHeader))
	if hop > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hop)
		defer cancel()
	}
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
