package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/outbound"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/maximhq/bifrost/core/schemas"
)

type systemOneHandler struct{ *shared }

func (h systemOneHandler) serveSystemOne(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, chatMaxBody))
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSystemOne", "read body"))
		return
	}
	if err := validateSystemOneBody(body); err != nil {
		h.writeHandlerError(w, err)
		return
	}
	model, err := bodyModel("gateway.serveSystemOne", body, false)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}

	reg := h.router.Registry()
	cfg := reg.Config()
	if cfg.EffectiveSystemOneBackend() == "" {
		h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveSystemOne", "no systemone backend configured"))
		return
	}
	backendID, downstream, err := reg.ResolveSystemOne(model)
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSystemOne", "resolve backend"))
		return
	}
	publicModel := model
	if publicModel == "" {
		publicModel = prefixModelID(backendID, downstream)
	}
	if !h.ensureSystemOneAllowed(backendID, publicModel, downstream) {
		h.writeModelNotAllowed(w, publicModel)
		return
	}
	// The credential is resolved later, only on the path that sends one.
	backend, backendURL, err := h.lookupBackend("gateway.serveSystemOne", backendID)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.systemone", observability.LLMSpanAttrs{
		Model: publicModel, BackendID: backendID, BackendURL: backendURL, Downstream: downstream,
	})
	dialUpstream := backendID
	defer func() { observability.EndDialSpan(span, err, dialUpstream) }()
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, config.ChatPlain)
	hop = hopBudget(hop, config.DefaultTimeouts().Chat)
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	r = r.WithContext(ctx)

	call := systemOneCall{backend: backend, url: backendURL, downstream: downstream, publicModel: publicModel, body: body, hop: hop}
	err = h.upstreams.Execute(backendID, func() error {
		if backend.IsCloudflareExtension() {
			return h.proxyCloudflareSystemOne(w, r, call)
		}
		if usesBifrostSystemOne(backend) {
			return h.proxyBifrostSystemOne(w, r, call)
		}
		return h.proxySystemOnePassthrough(w, r, call)
	})
	if err != nil {
		h.writeUpstreamDialError(w, err, "", backendID, nil)
	}
}

// systemOneCall is one SystemOne decision request for a resolved backend.
type systemOneCall struct {
	backend     config.BackendDef
	url         string
	downstream  string
	publicModel string
	body        []byte
	hop         time.Duration
}

func usesBifrostSystemOne(b config.BackendDef) bool {
	return b.ID == string(schemas.OpenRouter) && b.HasCapability(config.CapSystemOne)
}

func (h systemOneHandler) proxyBifrostSystemOne(w http.ResponseWriter, r *http.Request, c systemOneCall) error {
	if h.router == nil {
		return derrors.New(derrors.CodeNotReady, "gateway.proxyBifrostSystemOne", "router not configured")
	}
	raw, err := h.router.SystemOneDecisionRaw(r.Context(), router.Call{BackendID: c.backend.ID, Model: c.downstream, Body: c.body, Timeout: c.hop})
	if err != nil {
		return gatewayErr(err, "gateway.proxyBifrostSystemOne", "decision").With("backend", c.backend.ID)
	}
	raw, err = rewriteSystemOneResponseModel(raw, c.publicModel)
	if err != nil {
		return gatewayErr(err, "gateway.proxyBifrostSystemOne", "rewrite model")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, writeErr := w.Write(raw)
	if writeErr != nil {
		return afterHeaders(r.Context(), writeErr, true, "gateway.proxyBifrostSystemOne", "write response")
	}
	return nil
}

func (h systemOneHandler) proxyCloudflareSystemOne(w http.ResponseWriter, r *http.Request, c systemOneCall) error {
	client, err := h.cloudflareClient(c.backend)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeNotReady, "gateway.proxyCloudflareSystemOne", "cloudflare client")
	}
	out, err := client.SystemOne(r.Context(), c.downstream, c.body)
	if err != nil {
		return gatewayErr(err, "gateway.proxyCloudflareSystemOne", "decision").With("backend", c.backend.ID)
	}
	out, err = rewriteSystemOneResponseModel(out, c.publicModel)
	if err != nil {
		return gatewayErr(err, "gateway.proxyCloudflareSystemOne", "rewrite model")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, writeErr := w.Write(out)
	if writeErr != nil {
		return afterHeaders(r.Context(), writeErr, true, "gateway.proxyCloudflareSystemOne", "write response")
	}
	return nil
}

func (h systemOneHandler) proxySystemOnePassthrough(w http.ResponseWriter, r *http.Request, c systemOneCall) error {
	rewritten, err := withBodyModel("gateway.proxySystemOnePassthrough", c.body, c.downstream)
	if err != nil {
		return gatewayErr(err, "gateway.proxySystemOnePassthrough", "rewrite model")
	}
	auth, authErr := backendAuth(c.backend)
	if authErr != nil {
		return gatewayErr(authErr, "gateway.proxySystemOnePassthrough", "backend auth").With("backend", c.backend.ID)
	}
	target := systemOneURL(c.url)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(rewritten))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxySystemOnePassthrough", "request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	} else if a := r.Header.Get("Authorization"); a != "" {
		req.Header.Set("Authorization", a)
	}

	client := h.client
	if client == nil {
		client = newChatProxyClient(h.timeouts.Max)
	}
	resp, err := outbound.DoOnce(r.Context(), outbound.DepGateway, client, req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxySystemOnePassthrough", "dial").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := readUpstream(resp.Body, chatMaxBody, "gateway.proxySystemOnePassthrough")
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		w.Header().Set("Content-Type", firstNonEmpty(resp.Header.Get("Content-Type"), "application/json"))
		w.WriteHeader(resp.StatusCode)
		if _, err := w.Write(raw); err != nil {
			return afterHeaders(r.Context(), err, true, "gateway.proxySystemOnePassthrough", "write upstream error")
		}
		// A 5xx counts against the breaker like the chat and embeddings proxies.
		return upstream.StatusFailure(resp.StatusCode)
	}
	publicID := prefixModelID(c.backend.ID, c.downstream)
	raw, err = rewriteSystemOneResponseModel(raw, publicID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, writeErr := w.Write(raw)
	if writeErr != nil {
		return afterHeaders(r.Context(), writeErr, true, "gateway.proxySystemOnePassthrough", "write response")
	}
	return nil
}

func validateSystemOneBody(body []byte) error {
	var payload struct {
		State     json.RawMessage `json:"state"`
		Questions json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, "gateway.validateSystemOneBody", "invalid json")
	}
	if isJSONNull(payload.State) {
		return derrors.New(derrors.CodeInvalid, "gateway.validateSystemOneBody", "state required")
	}
	if isJSONNull(payload.Questions) {
		return derrors.New(derrors.CodeInvalid, "gateway.validateSystemOneBody", "questions required")
	}
	var q map[string]json.RawMessage
	if err := json.Unmarshal(payload.Questions, &q); err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, "gateway.validateSystemOneBody", "questions must be an object")
	}
	if len(q) == 0 {
		return derrors.New(derrors.CodeInvalid, "gateway.validateSystemOneBody", "questions required")
	}
	return nil
}

func rewriteSystemOneResponseModel(body []byte, publicModel string) ([]byte, error) {
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" {
		return body, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, nil
	}
	payload[keyModel] = publicModel
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteSystemOneResponseModel", "marshal")
	}
	return out, nil
}

func systemOneURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/systemone"
	}
	return base + "/v1/systemone"
}
