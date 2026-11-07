package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

const adminAllowMaxBody = 64 << 10

type adminAllowRequest struct {
	Backend string `json:"backend"`
	Model   string `json:"model"`
}

type adminAllowResponse struct {
	Backend        string   `json:"backend"`
	Model          string   `json:"model"`
	PublicID       string   `json:"public_id"`
	AlreadyAllowed bool     `json:"already_allowed"`
	Allow          []string `json:"allow"`
}

// adminHandler serves the models allow overlay.
type adminHandler struct {
	responder
	registry    *router.Registry
	overlayPath string
	// models checks an allow against the backend's live list and refreshes its cache.
	models modelsHandler
}

type adminAllowOverlayResponse struct {
	Object   string                                `json:"object"`
	Backends map[string]config.AllowOverlayBackend `json:"backends"`
}

func (h adminHandler) serveModelsAllowOverlay(w http.ResponseWriter, r *http.Request) {
	file, err := config.LoadAllowOverlay(h.overlayPath)
	if err != nil {
		h.writeHandlerError(w, r, derrors.Wrap(err, derrors.CodeInternal, "gateway.serveModelsAllowOverlay", "load overlay"))
		return
	}
	if file.Backends == nil {
		file.Backends = make(map[string]config.AllowOverlayBackend)
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.writeJSON(w, http.StatusOK, adminAllowOverlayResponse{
		Object:   "allow_overlay",
		Backends: file.Backends,
	})
}

// refuseUnsafeAdminWrite guards an admin mutation (POST, and any later PUT or DELETE
// under /v1/admin/) against a cross-site browser request. A browser sends a
// text/plain or form POST to a loopback gateway without a CORS preflight, so on a
// keyless gateway any page the operator opens could write. Requiring
// application/json forces that preflight, which the gateway never grants, and a
// cross-site Origin is refused outright. Requests without Origin (curl, the CLI,
// code assistants, scripts) pass. It reports whether it wrote a refusal.
func (h adminHandler) refuseUnsafeAdminWrite(w http.ResponseWriter, r *http.Request, op string) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		h.writeErrorJSON(w, r, http.StatusUnsupportedMediaType, derrors.New(derrors.CodeInvalid, op,
			"Content-Type must be application/json (curl: use --json)"), "")
		return true
	}
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
		h.log.Warn("polypus: cross-site admin write refused", "origin", o, "path", r.URL.Path)
		h.writeErrorJSON(w, r, http.StatusForbidden, derrors.New(derrors.CodeUnauthorized, op,
			"cross-site request refused"), "")
		return true
	}
	return false
}

// sameOrigin reports whether the browser Origin names the host the request reached.
// Behind the tailscale proxy the public host and scheme arrive in X-Forwarded-Host
// and X-Forwarded-Proto. A cross-site page cannot set those headers without a
// preflight, so trusting them does not reopen the hole.
func sameOrigin(origin string, r *http.Request) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false // Includes the opaque "null" origin.
	}
	scheme := requestScheme(r)
	host := r.Host
	if fh := firstForwarded(r.Header.Get("X-Forwarded-Host")); fh != "" {
		host = fh
	}
	return strings.EqualFold(u.Scheme, scheme) &&
		strings.EqualFold(hostWithDefaultPort(u.Host, u.Scheme), hostWithDefaultPort(host, scheme))
}

// firstForwarded is the first value of a comma-separated X-Forwarded-* header.
func firstForwarded(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

// hostWithDefaultPort adds the scheme's default port when host has none, so
// "example.ts.net" and "example.ts.net:443" compare equal over https.
func hostWithDefaultPort(host, scheme string) string {
	if u, err := url.Parse("//" + host); err == nil && u.Port() != "" {
		return host
	}
	if strings.EqualFold(scheme, schemeHTTPS) {
		return host + ":443"
	}
	return host + ":80"
}

func (h adminHandler) serveModelsAllow(w http.ResponseWriter, r *http.Request) {
	if h.refuseUnsafeAdminWrite(w, r, "gateway.serveModelsAllow") {
		return
	}
	req, err := h.parseAllowRequest(w, r)
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	resp, err := h.allowModel(r.Context(), r.Header.Get("Authorization"), req)
	if errors.Is(err, errModelNotInInventory) {
		h.writeOpenAIError(w, http.StatusBadRequest, openaiError{
			Message: "model not in backend inventory", Type: errTypeInvalidRequest, Param: keyModel, Code: codeModelNotInInventory,
		})
		return
	}
	if err != nil {
		h.writeHandlerError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, resp)
}

// allowRequest is a validated admin allow request.
type allowRequest struct {
	backend config.BackendDef
	// model is the id as sent, down its downstream id and publicID its public id.
	model, down, publicID string
}

// parseAllowRequest reads and checks the body: both fields present, a configured
// backend with a models.allow gate, and a model prefix that names that backend.
func (h adminHandler) parseAllowRequest(w http.ResponseWriter, r *http.Request) (allowRequest, error) {
	const op = "gateway.parseAllowRequest"
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, adminAllowMaxBody))
	if err != nil {
		return allowRequest{}, derrors.Wrap(err, derrors.CodeInvalid, op, "read body")
	}
	var in adminAllowRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return allowRequest{}, derrors.Wrap(err, derrors.CodeInvalid, op, "invalid json")
	}
	backendID := strings.TrimSpace(in.Backend)
	model := strings.TrimSpace(in.Model)
	if backendID == "" || model == "" {
		return allowRequest{}, derrors.New(derrors.CodeInvalid, op, "backend and model required")
	}
	b, ok := h.registry.Backend(backendID)
	if !ok {
		return allowRequest{}, derrors.New(derrors.CodeNotFound, op, "backend not found").With("backend", backendID)
	}
	if b.Models == nil || !b.Models.HasAllowGate() {
		return allowRequest{}, derrors.New(derrors.CodeFailedPrecondition, op, "backend has no models.allow gate").
			With("backend", backendID)
	}
	down := config.NormalizeDownstream(backendID, model)
	if down == "" {
		return allowRequest{}, derrors.New(derrors.CodeInvalid, op, "invalid model")
	}
	if prefix, _, found := strings.Cut(model, "/"); found && prefix != "" && prefix != backendID {
		if _, ok := h.registry.Config().Backends[prefix]; ok {
			return allowRequest{}, derrors.New(derrors.CodeInvalid, op, "model backend prefix mismatch")
		}
	}
	return allowRequest{backend: b, model: model, down: down, publicID: prefixModelID(backendID, down)}, nil
}

// errModelNotInInventory is an allow for a model the backend's live list lacks.
var errModelNotInInventory = derrors.New(derrors.CodeInvalid, "gateway.allowModel", "model not in backend inventory")

// allowModel allows req's model: it checks the model against the backend's live
// list, writes the overlay, then the registry, and takes the overlay entry back
// when the registry refuses. inboundAuth reaches a local backend's list only.
func (h adminHandler) allowModel(ctx context.Context, inboundAuth string, req allowRequest) (adminAllowResponse, error) {
	const op = "gateway.allowModel"
	b, backendID := req.backend, req.backend.ID
	resp := adminAllowResponse{Backend: backendID, Model: req.down, PublicID: req.publicID}
	if b.IsModelAllowed(req.model) || b.IsModelAllowed(req.down) || b.IsModelAllowed(req.publicID) {
		resp.AlreadyAllowed = true
		resp.Allow = slices.Clone(b.Models.Allow)
		return resp, nil
	}
	budgeted, cancel := withModelsBudget(ctx, h.registry.Config())
	defer cancel()
	// Validate against a live list, which also refreshes this backend's cache entry
	// before the answer: once the allow lands, GET /v1/models lists the model at once.
	inventory := h.models.refreshBackend(budgeted, inboundAuth, b)
	h.models.cache.flush()
	if !modelInInventory(backendID, req.down, req.model, inventory) {
		if len(inventory) == 0 {
			return adminAllowResponse{}, derrors.New(derrors.CodeNotReady, op, "could not fetch backend inventory").
				With("backend", backendID)
		}
		return adminAllowResponse{}, errModelNotInInventory
	}
	if err := config.OverlayAdd(ctx, h.overlayPath, backendID, req.down); err != nil {
		return adminAllowResponse{}, derrors.Wrap(err, derrors.CodeInternal, op, "overlay")
	}
	if err := h.registry.AllowModel(backendID, req.down); err != nil {
		if rbErr := config.OverlayRemove(context.WithoutCancel(ctx), h.overlayPath, backendID, req.down); rbErr != nil {
			h.log.Warn("polypus: allow overlay rollback failed after AllowModel error",
				"backend", backendID, keyModel, req.down, "rollback_err", rbErr, "allow_err", err)
		}
		return adminAllowResponse{}, derrors.Wrap(err, derrors.CodeInternal, op, "allow model")
	}
	if b2, ok := h.registry.Backend(backendID); ok && b2.Models != nil {
		resp.Allow = slices.Clone(b2.Models.Allow)
	}
	return resp, nil
}

func modelInInventory(backendID, down, req string, models []openaiModel) bool {
	for _, m := range models {
		if m.ID == req || m.ID == down || m.ID == prefixModelID(backendID, down) {
			return true
		}
		if config.NormalizeDownstream(backendID, m.ID) == down {
			return true
		}
	}
	return false
}
