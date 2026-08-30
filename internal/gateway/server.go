package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/router"
	"github.com/behaviorengineering/polypus/internal/switchyard"
)

// shared holds dependencies used by capability handlers.
type shared struct {
	opts     config.ServeOptions
	router   *router.Client
	proxy    http.Handler
	invCache *modelsInventoryCache
	timeouts config.Timeouts
	client   *http.Client
	swProbe  switchyardProbeCache
}

// Gateway is the Polypus HTTP mux (controller) over capability handlers.
type Gateway struct {
	*shared
}

type chatHandler struct{ *shared }
type modelsHandler struct{ *shared }
type healthHandler struct{ *shared }
type speechHandler struct{ *shared }

// NewHandler returns the public Polypus HTTP handler (*Gateway).
// Switchyard TOML write is startup I/O (not capability wiring); a later
// change may move it to an explicit serve/process-compose hook.
func NewHandler(opts config.ServeOptions) (http.Handler, error) {
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	rc, err := router.NewClient(rcfg)
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}
	if _, err := switchyard.WriteConfigIfNeeded(rcfg, opts.GatewayBaseURL()); err != nil {
		rc.Close()
		return nil, fmt.Errorf("gateway: switchyard render: %w", err)
	}
	proxyURL := rc.Registry().ProxyBackendURL()
	proxy, err := newFallbackProxy(proxyURL)
	if err != nil {
		rc.Close()
		return nil, err
	}
	timeouts := rcfg.Timeouts
	if timeouts.Max == 0 {
		timeouts = config.DefaultTimeouts()
	}
	s := &shared{
		opts:     opts,
		router:   rc,
		proxy:    proxy,
		invCache: newModelsInventoryCache(),
		timeouts: timeouts,
		client:   newChatProxyClient(timeouts.Max),
	}
	return &Gateway{shared: s}, nil
}

func newFallbackProxy(backendURL string) (http.Handler, error) {
	target, err := url.Parse(backendURL)
	if err != nil {
		return nil, fmt.Errorf("backend url: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, fmt.Sprintf("polypus backend unavailable: %v", err), http.StatusBadGateway)
	}
	origDirector := proxy.Director
	proxy.Director = func(r *http.Request) {
		origDirector(r)
		r.Host = target.Host
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
		http.Error(w, "polypus: backend not found", http.StatusBadGateway)
		return
	}
	backend, _ := reg.Backend(backendID)
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
	if err = proxyChatCompletions(w, r, backendURL, rewritten, h.client, hop, backendAuth); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
}

func (h chatHandler) serveNamedRouterChat(w http.ResponseWriter, r *http.Request, body []byte, model, routerName string, vision bool) {
	reg := h.router.Registry()
	cfg := reg.Config()
	router, ok := lookupRouter(cfg, routerName)
	if !ok {
		http.Error(w, fmt.Sprintf("polypus: unknown router %q", model), http.StatusBadRequest)
		return
	}
	if router.Capability != config.CapChat {
		http.Error(w, fmt.Sprintf("polypus: router %q does not support chat", model), http.StatusBadRequest)
		return
	}
	if vision {
		http.Error(w, fmt.Sprintf("polypus: router %q does not support vision (v1 chat-only)", model), http.StatusBadRequest)
		return
	}

	var err error
	switch router.Route.Type {
	case config.RoutePassthrough:
		backendID, downstream, resolveErr := reg.ResolveChat(router.Route.Target)
		if resolveErr != nil {
			http.Error(w, resolveErr.Error(), http.StatusBadRequest)
			return
		}
		if !h.ensureModelAllowed(backendID, router.Route.Target) {
			writeModelNotAllowed(w, router.Route.Target)
			return
		}
		backendURL, ok := reg.BackendURL(backendID)
		if !ok {
			http.Error(w, "polypus: backend not found", http.StatusBadGateway)
			return
		}
		backend, _ := reg.Backend(backendID)
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
		ctx, span := observability.StartLLMSpan(r.Context(), "polypus.chat", model, backendID, backendURL, downstream)
		defer func() { observability.EndSpan(span, err) }()
		r = r.WithContext(ctx)
		hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, false, chatBodyWantsThinking(body))
		if err = proxyChatCompletions(w, r, backendURL, rewritten, h.client, hop, backendAuth); err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
	case config.RouteStageRouter:
		switchyardURL := cfg.EffectiveSwitchyardBaseURL()
		if probeErr := h.swProbe.available(r.Context(), switchyardURL); probeErr != nil {
			http.Error(w, "polypus: switchyard unavailable: "+probeErr.Error(), http.StatusServiceUnavailable)
			return
		}
		ctx, span := observability.StartRouterSpan(r.Context(), "polypus.router", model, routerName, switchyardURL)
		defer func() { observability.EndSpan(span, err) }()
		r = r.WithContext(ctx)
		hop := h.timeouts.Max
		if hop <= 0 {
			hop = config.DefaultTimeouts().Max
		}
		if err = proxyChatCompletionsOpts(w, r, switchyardURL, body, h.client, hop, "", false); err != nil {
			h.swProbe.invalidate()
			code := http.StatusBadGateway
			if isSwitchyardUnreachable(err) {
				code = http.StatusServiceUnavailable
			}
			http.Error(w, err.Error(), code)
		}
	default:
		http.Error(w, fmt.Sprintf("polypus: router %q has unsupported route type", model), http.StatusBadRequest)
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
	defer resp.Body.Close()
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
		http.Error(w, "polypus: backend not found", http.StatusBadGateway)
		return
	}
	backend, _ := reg.Backend(backendID)
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
	if err = proxyEmbeddings(w, r, backendURL, rewritten, h.client, hop, backendAuth); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
}

func (h speechHandler) serveSpeech(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
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
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
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
		backendURL, _ = h.router.Registry().BackendURL(string(backendID))
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.speech", req.Model, string(backendID), backendURL, downstream)
	defer func() { observability.EndSpan(span, err) }()
	audio, err := h.router.Synthesize(ctx, router.SpeechRequest{
		Model:          req.Model,
		Input:          req.Input,
		Voice:          req.Voice,
		ResponseFormat: req.ResponseFormat,
		Speed:          req.Speed,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", speechContentType(req.ResponseFormat))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

func (h speechHandler) serveTranscription(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file required: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	audio, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read file: "+err.Error(), http.StatusBadRequest)
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
		backendURL, _ = h.router.Registry().BackendURL(string(backendID))
	}
	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.transcription", sttModel, string(backendID), backendURL, downstream)
	defer func() { observability.EndSpan(span, err) }()
	out, ct, err := h.router.Transcribe(ctx, router.TranscriptionRequest{
		Model:          sttModel,
		Audio:          audio,
		Filename:       filename,
		ResponseFormat: format,
		Language:       r.FormValue("language"),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
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

// Close releases router resources.
func (g *Gateway) Close() {
	if g != nil && g.shared != nil && g.router != nil {
		g.router.Close()
	}
}

// ListenAndServe starts the gateway on opts.ListenAddr().
func ListenAndServe(opts config.ServeOptions) error {
	handler, err := NewHandler(opts)
	if err != nil {
		return err
	}
	if g, ok := handler.(*Gateway); ok {
		defer g.Close()
	}
	server := &http.Server{
		Addr:              opts.ListenAddr(),
		Handler:           observability.WrapHandler(handler),
		ReadHeaderTimeout: 30 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			return err
		}
		return nil
	}
}
