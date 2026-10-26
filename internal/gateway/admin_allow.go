package gateway

import (
	"encoding/json"
	"io"
	"net/http"
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

func (h adminHandler) serveModelsAllow(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, adminAllowMaxBody))
	if err != nil {
		h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "invalid", "read body")
		return
	}
	var req adminAllowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "invalid", "invalid json")
		return
	}
	backendID := strings.TrimSpace(req.Backend)
	model := strings.TrimSpace(req.Model)
	if backendID == "" || model == "" {
		h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "invalid", "backend and model required")
		return
	}
	reg := h.router.Registry()
	b, ok := reg.Backend(backendID)
	if !ok {
		h.writeAdminJSONError(w, http.StatusNotFound, errTypeInvalidRequest, "not_found", "backend not found")
		return
	}
	if b.Models == nil || !b.Models.HasAllowGate() {
		h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "failed_precondition", "backend has no models.allow gate")
		return
	}
	down := config.NormalizeDownstream(backendID, model)
	if down == "" {
		h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "invalid", "invalid model")
		return
	}
	if i := strings.Index(model, "/"); i > 0 {
		prefix := model[:i]
		if prefix != backendID {
			if _, ok := reg.Config().Backends[prefix]; ok {
				h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "invalid", "model backend prefix mismatch")
				return
			}
		}
	}
	publicID := prefixModelID(backendID, down)
	if b.IsModelAllowed(model) || b.IsModelAllowed(down) || b.IsModelAllowed(publicID) {
		h.writeAdminAllowOK(w, backendID, down, publicID, true, b.Models.Allow)
		return
	}
	budgeted, cancel := withModelsBudget(r.Context(), h.router.Registry().Config())
	defer cancel()
	inventory := modelsHandler(h).inventoryForBackend(budgeted, r.Header.Get("Authorization"), b)
	h.invCache.flush()
	if !modelInInventory(backendID, down, model, inventory) {
		if len(inventory) == 0 {
			h.writeAdminJSONError(w, http.StatusServiceUnavailable, errTypeInvalidRequest, "unavailable", "could not fetch backend inventory")
			return
		}
		h.writeAdminJSONError(w, http.StatusBadRequest, errTypeInvalidRequest, "model_not_in_inventory", "model not in backend inventory")
		return
	}
	if err := config.OverlayAdd(h.overlayPath, backendID, down); err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, "gateway.serveModelsAllow", "overlay"))
		return
	}
	if err := reg.AllowModel(backendID, down); err != nil {
		if rbErr := config.OverlayRemove(h.overlayPath, backendID, down); rbErr != nil {
			h.log.Warn("allow overlay rollback failed after AllowModel error",
				"backend", backendID, keyModel, down, "rollback_err", rbErr, "allow_err", err)
		}
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, "gateway.serveModelsAllow", "allow model"))
		return
	}
	b2, ok := reg.Backend(backendID)
	if !ok || b2.Models == nil {
		h.writeAdminAllowOK(w, backendID, down, publicID, false, nil)
		return
	}
	h.writeAdminAllowOK(w, backendID, down, publicID, false, b2.Models.Allow)
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

func (h adminHandler) writeAdminJSONError(w http.ResponseWriter, status int, typ, code, msg string) {
	h.writeOpenAIError(w, status, openaiError{Message: msg, Type: typ, Code: code})
}

func (h adminHandler) writeAdminAllowOK(w http.ResponseWriter, backend, down, publicID string, already bool, allow []string) {
	h.writeJSON(w, http.StatusOK, adminAllowResponse{
		Backend:        backend,
		Model:          down,
		PublicID:       publicID,
		AlreadyAllowed: already,
		Allow:          append([]string(nil), allow...),
	})
}
