package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/gateway/router"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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

	reg := h.registry
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
	// The router and the Cloudflare client resolve the credential themselves.
	backend, backendURL, err := h.lookupBackend("gateway.serveSystemOne", backendID)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.systemone", observability.LLMSpanAttrs{
		Model: publicModel, BackendID: backendID, BackendURL: backendURL, Downstream: downstream,
	})
	dialUpstream := backendID
	defer func() { endDialSpan(span, err, dialUpstream) }()
	hop := h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, config.ChatPlain)
	hop = hopBudget(hop, config.DefaultTimeouts().Chat)
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	r = r.WithContext(ctx)

	call := systemOneCall{backend: backend, url: backendURL, downstream: downstream, publicModel: publicModel, body: body, hop: hop}
	// Config load rejects a systemone backend with no decision route; this guards
	// an injected config the same way, before the breaker sees the call.
	if !backend.IsCloudflareExtension() && !usesBifrostSystemOne(backend) {
		err = derrors.New(derrors.CodeFailedPrecondition, "gateway.serveSystemOne", "backend has no systemone route").
			With("backend", backendID)
		h.writeHandlerError(w, err)
		return
	}
	err = h.upstreams.Execute(backendID, func() error {
		if backend.IsCloudflareExtension() {
			return h.proxyCloudflareSystemOne(w, r, call)
		}
		return h.proxyBifrostSystemOne(w, r, call)
	})
	if err != nil {
		h.writeUpstreamDialError(w, dialFailure{err: err, upstream: backendID})
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

// usesBifrostSystemOne reports whether the backend's Bifrost provider implements the
// decision operation: native OpenRouter, or Typesafe through a typesafe extension.
func usesBifrostSystemOne(b config.BackendDef) bool {
	return b.HasCapability(config.CapSystemOne) &&
		(b.ID == string(schemas.OpenRouter) || b.IsTypesafeExtension())
}

func (h systemOneHandler) proxyBifrostSystemOne(w http.ResponseWriter, r *http.Request, c systemOneCall) error {
	raw, err := h.systemOne.SystemOneDecisionRaw(r.Context(), router.Call{BackendID: c.backend.ID, Model: c.downstream, Body: c.body, Timeout: c.hop})
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
	// RawMessage values pass through byte for byte, so numbers keep their precision.
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil || payload == nil {
		return body, nil
	}
	model, err := json.Marshal(publicModel)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteSystemOneResponseModel", "marshal model")
	}
	payload[keyModel] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteSystemOneResponseModel", "marshal")
	}
	return out, nil
}
