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
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/maximhq/bifrost/core/schemas"
)

// systemOneHandler serves SystemOne decisions.
type systemOneHandler struct {
	responder
	registry  *router.Registry
	systemOne systemOneRouter
	clients   backendClients
	upstreams *upstream.Board
	timeouts  config.Timeouts
}

func (h systemOneHandler) serveSystemOne(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, chatMaxBody))
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveSystemOne", "read body"))
		return
	}
	if err := validateSystemOneBody(body); err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	model, err := bodyModel("gateway.serveSystemOne", body, false)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}

	reg := h.registry
	cfg := reg.Config()
	if cfg.EffectiveSystemOneBackend() == "" {
		h.writeHandlerError(w, r, derrors.New(derrors.CodeNotReady, "gateway.serveSystemOne", "no systemone backend configured"))
		return
	}
	backendID, downstream, err := reg.ResolveSystemOne(model)
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "gateway.serveSystemOne", "resolve backend"))
		return
	}
	publicModel := model
	if publicModel == "" {
		publicModel = prefixModelID(backendID, downstream)
	}
	if !ensureSystemOneAllowed(h.registry, backendID, publicModel, downstream) {
		h.writeModelNotAllowed(w, publicModel)
		return
	}
	// The router and the Cloudflare client resolve the credential themselves.
	backend, backendURL, err := lookupBackend(h.registry, "gateway.serveSystemOne", backendID)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}

	ctx, span := observability.StartLLMSpan(r.Context(), "polypus.systemone", observability.LLMSpanAttrs{
		Model: publicModel, BackendID: backendID, BackendURL: backendURL, Downstream: downstream,
	})
	dialUpstream := backendID
	defer func() { endDialSpan(span, err, dialUpstream) }()
	hop := hopBudget(h.timeouts.ResolveChat(r.Header.Get(config.TimeoutHeader), backendID, config.ChatPlain), config.DefaultTimeouts().Chat)
	ctx = callerBudget(ctx, hop, hopBudget(h.timeouts.ResolveChat("", backendID, config.ChatPlain), config.DefaultTimeouts().Chat))
	ctx, cancel := context.WithTimeout(ctx, hop)
	defer cancel()
	r = r.WithContext(ctx)

	call := systemOneCall{backend: backend, downstream: downstream, body: body, hop: hop}
	// Everything local happens outside the breaker, so it answers as itself and never
	// counts against the backend: picking the route and building the client before
	// the dial, and rewriting the model after it.
	decide, err := h.systemOneDial(call)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	var raw []byte
	err = h.upstreams.Execute(ctx, backendID, func() error {
		var dialErr error
		raw, dialErr = decide(ctx)
		return dialErr
	})
	if err != nil {
		h.writeUpstreamDialError(w, r, dialFailure{err: err, upstream: backendID})
		return
	}
	raw, err = rewriteSystemOneResponseModel(raw, publicModel)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, writeErr := w.Write(raw); writeErr != nil {
		// The status is out, so the failure is only recorded on the span.
		err = afterHeaders(ctx, writeErr, true, "gateway.serveSystemOne", "write response")
	}
}

// systemOneCall is one SystemOne decision request for a resolved backend.
type systemOneCall struct {
	backend    config.BackendDef
	downstream string
	body       []byte
	hop        time.Duration
}

// systemOneDecide sends one decision request and returns the backend's raw answer.
type systemOneDecide func(ctx context.Context) ([]byte, error)

// systemOneDial picks the decision route for c's backend and builds what it needs.
// Config load rejects a systemone backend with no decision route; this guards an
// injected config the same way.
func (h systemOneHandler) systemOneDial(c systemOneCall) (systemOneDecide, error) {
	const op = "gateway.systemOneDial"
	switch {
	case c.backend.IsCloudflareExtension():
		client, err := h.clients.cloudflareClient(c.backend)
		if err != nil {
			return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), op, "cloudflare client").
				With("backend", c.backend.ID)
		}
		return func(ctx context.Context) ([]byte, error) {
			out, err := client.SystemOne(ctx, c.downstream, c.body)
			if err != nil {
				return nil, gatewayErr(err, "gateway.proxyCloudflareSystemOne", "decision").With("backend", c.backend.ID)
			}
			return out, nil
		}, nil
	case usesBifrostSystemOne(c.backend):
		return func(ctx context.Context) ([]byte, error) {
			raw, err := h.systemOne.SystemOneDecisionRaw(ctx, router.Call{BackendID: c.backend.ID, Model: c.downstream, Body: c.body, Timeout: c.hop})
			if err != nil {
				return nil, gatewayErr(err, "gateway.proxyBifrostSystemOne", "decision").With("backend", c.backend.ID)
			}
			return raw, nil
		}, nil
	default:
		return nil, derrors.New(derrors.CodeFailedPrecondition, op, "backend has no systemone route").
			With("backend", c.backend.ID)
	}
}

// usesBifrostSystemOne reports whether the backend's Bifrost provider implements the
// decision operation: native OpenRouter, or Typesafe through a typesafe extension.
func usesBifrostSystemOne(b config.BackendDef) bool {
	return b.HasCapability(config.CapSystemOne) &&
		(b.ID == string(schemas.OpenRouter) || b.IsTypesafeExtension())
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
