package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
)

type systemOneHandler struct{ *shared }

func (h systemOneHandler) serveSystemOne(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, chatMaxBody))
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSystemOne", "read body"))
		return
	}
	if err := validateSystemOneBody(body); err != nil {
		writeHandlerError(w, err)
		return
	}
	model, err := extractSystemOneModel(body)
	if err != nil {
		writeHandlerError(w, err)
		return
	}

	reg := h.router.Registry()
	cfg := reg.Config()
	if cfg.EffectiveSystemOneBackend() == "" {
		writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveSystemOne", "no systemone backend configured"))
		return
	}
	backendID, downstream, err := reg.ResolveSystemOne(model)
	if err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSystemOne", "resolve backend"))
		return
	}
	publicModel := model
	if publicModel == "" {
		publicModel = prefixModelID(backendID, downstream)
	}
	if !h.ensureModelAllowed(backendID, publicModel) {
		writeModelNotAllowed(w, publicModel)
		return
	}
	backend, ok := reg.Backend(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveSystemOne", errBackendNotFound).
			With("backend", backendID))
		return
	}
	backendURL, ok := reg.BackendURL(backendID)
	if !ok {
		writeHandlerError(w, derrors.New(derrors.CodeUnavailable, "gateway.serveSystemOne", errBackendNotFound).
			With("backend", backendID))
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.systemone", publicModel, backendID, backendURL, downstream)
	defer func() { observability.EndSpan(span, err) }()
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, false, false)
	if hop > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hop)
		defer cancel()
	}
	r = r.WithContext(ctx)

	err = h.upstreams.Execute(backendID, func() error {
		if backend.IsCloudflareExtension() {
			return h.proxyCloudflareSystemOne(w, r, backend, downstream, body, publicModel)
		}
		return h.proxySystemOnePassthrough(w, r, backend, backendURL, downstream, body)
	})
	if err != nil {
		writeUpstreamDialError(w, err, "", nil)
	}
}

func (h systemOneHandler) proxyCloudflareSystemOne(w http.ResponseWriter, r *http.Request, backend config.BackendDef, downstream string, body []byte, publicModel string) error {
	client, err := h.cloudflareClient(backend)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeNotReady, "gateway.proxyCloudflareSystemOne", "cloudflare client")
	}
	out, err := client.SystemOne(r.Context(), downstream, body)
	if err != nil {
		return err
	}
	out, err = rewriteSystemOneResponseModel(out, publicModel)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, writeErr := w.Write(out)
	if writeErr != nil {
		return derrors.Wrap(writeErr, derrors.CodeInternal, "gateway.proxyCloudflareSystemOne", "write response")
	}
	return nil
}

func (h systemOneHandler) proxySystemOnePassthrough(w http.ResponseWriter, r *http.Request, backend config.BackendDef, backendURL, downstream string, body []byte) error {
	rewritten, err := rewriteSystemOneModel(body, downstream)
	if err != nil {
		return err
	}
	auth, authErr := mustBackendAuth(backend)
	if authErr != nil {
		return authErr
	}
	target := systemOneURL(backendURL)
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
	resp, err := client.Do(req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxySystemOnePassthrough", "dial").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, chatMaxBody))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxySystemOnePassthrough", "read body")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		w.Header().Set("Content-Type", firstNonEmpty(resp.Header.Get("Content-Type"), "application/json"))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(raw)
		return nil
	}
	publicID := prefixModelID(backend.ID, downstream)
	raw, err = rewriteSystemOneResponseModel(raw, publicID)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, writeErr := w.Write(raw)
	if writeErr != nil {
		return derrors.Wrap(writeErr, derrors.CodeInternal, "gateway.proxySystemOnePassthrough", "write response")
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
	if len(bytes.TrimSpace(payload.State)) == 0 || string(bytes.TrimSpace(payload.State)) == "null" {
		return derrors.New(derrors.CodeInvalid, "gateway.validateSystemOneBody", "state required")
	}
	if len(bytes.TrimSpace(payload.Questions)) == 0 || string(bytes.TrimSpace(payload.Questions)) == "null" {
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

func extractSystemOneModel(body []byte) (string, error) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInvalid, "gateway.extractSystemOneModel", "invalid json")
	}
	return strings.TrimSpace(payload.Model), nil
}

func rewriteSystemOneModel(body []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "gateway.rewriteSystemOneModel", "invalid json")
	}
	payload["model"] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteSystemOneModel", "marshal")
	}
	return out, nil
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
	payload["model"] = publicModel
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
