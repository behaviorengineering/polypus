package gateway

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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

type adminHandler struct{ *shared }

type adminAllowOverlayResponse struct {
	Object   string                                `json:"object"`
	Backends map[string]config.AllowOverlayBackend `json:"backends"`
}

func (h adminHandler) serveModelsAllowOverlay(w http.ResponseWriter, r *http.Request) {
	file, err := config.LoadAllowOverlay(h.overlayPath)
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, "gateway.serveModelsAllowOverlay", "load overlay"))
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
		h.writeErrorJSON(w, http.StatusUnsupportedMediaType, derrors.New(derrors.CodeInvalid, op,
			"Content-Type must be application/json (curl: use --json)"), "")
		return true
	}
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r) {
		h.log.Warn("polypus: cross-site admin write refused", "origin", o, "path", r.URL.Path)
		h.writeErrorJSON(w, http.StatusForbidden, derrors.New(derrors.CodeUnauthorized, op,
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
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := firstForwarded(r.Header.Get("X-Forwarded-Proto")); p != "" {
		scheme = p
	}
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
	if strings.EqualFold(scheme, "https") {
		return host + ":443"
	}
	return host + ":80"
}

func (h adminHandler) serveModelsAllow(w http.ResponseWriter, r *http.Request) {
	const op = "gateway.serveModelsAllow"
	if h.refuseUnsafeAdminWrite(w, r, op) {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, adminAllowMaxBody))
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, op, "read body"))
		return
	}
	var req adminAllowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, op, "invalid json"))
		return
	}
	backendID := strings.TrimSpace(req.Backend)
	model := strings.TrimSpace(req.Model)
	if backendID == "" || model == "" {
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, op, "backend and model required"))
		return
	}
	reg := h.registry
	b, ok := reg.Backend(backendID)
	if !ok {
		h.writeHandlerError(w, derrors.New(derrors.CodeNotFound, op, "backend not found").With("backend", backendID))
		return
	}
	if b.Models == nil || !b.Models.HasAllowGate() {
		h.writeHandlerError(w, derrors.New(derrors.CodeFailedPrecondition, op, "backend has no models.allow gate").
			With("backend", backendID))
		return
	}
	down := config.NormalizeDownstream(backendID, model)
	if down == "" {
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, op, "invalid model"))
		return
	}
	if i := strings.Index(model, "/"); i > 0 {
		prefix := model[:i]
		if prefix != backendID {
			if _, ok := reg.Config().Backends[prefix]; ok {
				h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, op, "model backend prefix mismatch"))
				return
			}
		}
	}
	publicID := prefixModelID(backendID, down)
	if b.IsModelAllowed(model) || b.IsModelAllowed(down) || b.IsModelAllowed(publicID) {
		h.writeJSON(w, http.StatusOK, adminAllowResponse{Backend: backendID, Model: down, PublicID: publicID, AlreadyAllowed: true, Allow: slices.Clone(b.Models.Allow)})
		return
	}
	budgeted, cancel := withModelsBudget(r.Context(), reg.Config())
	defer cancel()
	// Validate against a live list, which also refreshes this backend's cache entry
	// before the answer: once the allow lands, GET /v1/models lists the model at once.
	inventory := modelsHandler(h).refreshBackend(budgeted, r.Header.Get("Authorization"), b)
	h.invCache.flush()
	if !modelInInventory(backendID, down, model, inventory) {
		if len(inventory) == 0 {
			h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, op, "could not fetch backend inventory").
				With("backend", backendID))
			return
		}
		h.writeOpenAIError(w, http.StatusBadRequest, openaiError{
			Message: "model not in backend inventory", Type: errTypeInvalidRequest, Param: keyModel, Code: codeModelNotInInventory,
		})
		return
	}
	if err := config.OverlayAdd(r.Context(), h.overlayPath, backendID, down); err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, op, "overlay"))
		return
	}
	if err := reg.AllowModel(backendID, down); err != nil {
		if rbErr := config.OverlayRemove(context.WithoutCancel(r.Context()), h.overlayPath, backendID, down); rbErr != nil {
			h.log.Warn("allow overlay rollback failed after AllowModel error",
				"backend", backendID, keyModel, down, "rollback_err", rbErr, "allow_err", err)
		}
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, op, "allow model"))
		return
	}
	b2, ok := reg.Backend(backendID)
	if !ok || b2.Models == nil {
		h.writeJSON(w, http.StatusOK, adminAllowResponse{Backend: backendID, Model: down, PublicID: publicID})
		return
	}
	h.writeJSON(w, http.StatusOK, adminAllowResponse{Backend: backendID, Model: down, PublicID: publicID, Allow: slices.Clone(b2.Models.Allow)})
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
