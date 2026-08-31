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

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/extension/cloudflare"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/router"
	"github.com/behaviorengineering/polypus/internal/upstream"
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
}

// Gateway is the Polypus HTTP mux (controller) over capability handlers.
type Gateway struct {
	*shared
}

type chatHandler struct{ *shared }
type modelsHandler struct{ *shared }
type healthHandler struct{ *shared }
type speechHandler struct{ *shared }

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

	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}

	cfGet := ho.cfGet
	if cfGet == nil {
		cfGet = cloudflare.GetClient
	}

	var rc Router
	owned := false
	if ho.router != nil {
		rc = ho.router
	} else {
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
	}
	return &Gateway{shared: s}, nil
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
	case r.URL.Path == "/health" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		healthHandler{g.shared}.serveHealth(w, r)
	case r.URL.Path == "/health/backends" && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		healthHandler{g.shared}.serveBackendHealth(w, r)
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
	default:
		http.NotFound(w, r)
	}
}

func (h chatHandler) serveChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, chatMaxBody))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	model, err := extractChatModel(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		if cfg.DefaultChatBackend == "" {
			http.Error(w, "polypus: no chat backend configured", http.StatusServiceUnavailable)
			return
		}
		backendID, downstream, err = reg.ResolveChat(model)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.ensureModelAllowed(backendID, model) {
		writeModelNotAllowed(w, model)
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		http.Error(w, errBackendNotFound, http.StatusBadGateway)
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		http.Error(w, errBackendNotFound, http.StatusBadGateway)
		return
	}
	backendAuth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		http.Error(w, authErr.Error(), http.StatusServiceUnavailable)
		return
	}
	rewritten, err := rewriteChatModel(body, downstream)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.chat", model, backendID, backendURL, downstream)
	defer func() { observability.EndSpan(span, err) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, vision, chatBodyWantsThinking(body))
	if err = h.proxyOrBifrostChat(w, r, backendID, downstream, backendURL, rewritten, hop, backendAuth, true); err != nil {
		writeUpstreamDialError(w, err, "", nil)
		return
	}
}

func (h chatHandler) serveNamedRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, routerName string, vision bool) {
	reg := h.router.Registry()
	cfg := reg.Config()
	nr, ok := lookupRouter(cfg, routerName)
	if !ok {
		http.Error(w, fmt.Sprintf("polypus: unknown router %q", model), http.StatusBadRequest)
		return
	}
	if msg := validateNamedRouterForChat(nr, model, vision); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}

	switch classifyNamedRouterRoute(nr.Route.Type) {
	case dispatchPassthrough:
		h.servePassthroughRouterChat(w, r, body, model, nr.Route.Target)
	case dispatchSwitchyard:
		h.serveSwitchyardRouterChat(w, r, body, model, routerName, cfg.EffectiveSwitchyardBaseURL())
	default:
		http.Error(w, fmt.Sprintf("polypus: router %q has unsupported route type", model), http.StatusBadRequest)
	}
}

func (h chatHandler) servePassthroughRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, target string) {
	reg := h.router.Registry()
	backendID, downstream, resolveErr := reg.ResolveChat(target)
	if resolveErr != nil {
		http.Error(w, resolveErr.Error(), http.StatusBadRequest)
		return
	}
	if !h.ensureModelAllowed(backendID, target) {
		writeModelNotAllowed(w, target)
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		http.Error(w, errBackendNotFound, http.StatusBadGateway)
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		http.Error(w, errBackendNotFound, http.StatusBadGateway)
		return
	}
	backendAuth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		http.Error(w, authErr.Error(), http.StatusServiceUnavailable)
		return
	}
	rewritten, rewriteErr := rewriteChatModel(body, downstream)
	if rewriteErr != nil {
		http.Error(w, rewriteErr.Error(), http.StatusBadRequest)
		return
	}
	var err error
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.chat", model, backendID, backendURL, downstream)
	defer func() { observability.EndSpan(span, err) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, false, chatBodyWantsThinking(body))
	if err = h.proxyOrBifrostChat(w, r, backendID, downstream, backendURL, rewritten, hop, backendAuth, true); err != nil {
		writeUpstreamDialError(w, err, "", nil)
	}
}

func (h chatHandler) serveSwitchyardRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, routerName, switchyardURL string) {
	var err error
	ctx, span := observability.StartRouterSpan(r.Context(), "polypus.router", model, routerName, switchyardURL)
	defer func() { observability.EndSpan(span, err) }()
	r = r.WithContext(ctx)
	hop := h.timeouts.Max
	if hop <= 0 {
		hop = config.DefaultTimeouts().Max
	}
	downstream := model
	if mid, midErr := extractChatModel(body); midErr == nil && strings.TrimSpace(mid) != "" {
		downstream = mid
	}
	err = h.upstreams.Execute(upstream.NameSwitchyard, func() error {
		if !h.router.UsesBifrost(router.ProviderSwitchyard) {
			return proxyChatCompletionsOpts(w, r, switchyardURL, body, h.client, hop, "", false)
		}
		return h.bifrostChatResponse(w, r, router.ProviderSwitchyard, downstream, body, hop, false)
	})
	if err != nil {
		writeUpstreamDialError(w, err, "polypus: switchyard unavailable: ", isSwitchyardUnreachable)
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
	http.Error(w, err.Error(), derrors.HTTPStatus(err))
}

// writeUpstreamDialError writes a dial failure unless the upstream body was already sent.
// prefix is prepended for Switchyard-style messages; unreachable maps to 503 when set.
// Typed domain errors use HTTPStatus; other errors stay 502 unless the breaker or unreachable hook says 503.
func writeUpstreamDialError(w http.ResponseWriter, err error, prefix string, unreachable func(error) bool) {
	if err == nil || upstream.ResponseWritten(err) {
		return
	}
	msg := err.Error()
	if prefix != "" {
		msg = prefix + msg
	}
	code := http.StatusBadGateway
	var de *derrors.Error
	if errors.As(err, &de) {
		code = derrors.HTTPStatus(err)
	}
	if upstream.Unavailable(err) || (unreachable != nil && unreachable(err)) {
		code = http.StatusServiceUnavailable
	}
	http.Error(w, msg, code)
}

func (h chatHandler) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, embedMaxBody))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	model, err := extractEmbedModel(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	reg := h.router.Registry()
	cfg := reg.Config()
	if cfg.DefaultEmbedBackend == "" {
		http.Error(w, "polypus: no embed backend configured", http.StatusServiceUnavailable)
		return
	}
	backendID, downstream, err := reg.ResolveEmbed(model)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !h.ensureModelAllowed(backendID, model) {
		writeModelNotAllowed(w, model)
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		http.Error(w, errBackendNotFound, http.StatusBadGateway)
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		http.Error(w, errBackendNotFound, http.StatusBadGateway)
		return
	}
	backendAuth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		http.Error(w, authErr.Error(), http.StatusServiceUnavailable)
		return
	}
	rewritten, err := rewriteEmbedModel(body, downstream)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.embeddings", model, backendID, backendURL, downstream)
	defer func() { observability.EndSpan(span, err) }()
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
				return fmt.Errorf("write response: %w", writeErr)
			}
			return nil
		}
		return proxyEmbeddings(w, r, backendURL, rewritten, h.client, hop, backendAuth)
	})
	if err != nil {
		writeUpstreamDialError(w, err, "", nil)
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
	defer func() { observability.EndSpan(span, err) }()
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
		writeUpstreamDialError(w, err, "", nil)
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
	defer func() { observability.EndSpan(span, err) }()
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
		writeUpstreamDialError(w, err, "", nil)
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
