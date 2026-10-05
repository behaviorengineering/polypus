package gateway

import (
	"encoding/json"
	"io"
	"log/slog"
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

func (h adminHandler) serveModelsAllow(w http.ResponseWriter, r *http.Request) {
	if h.adminKeys == nil {
		writeAdminUnauthorized(w)
		return
	}
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		writeAdminUnauthorized(w)
		return
	}
	if _, err := h.adminKeys.Verify(token); err != nil {
		writeAdminUnauthorized(w)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, adminAllowMaxBody))
	if err != nil {
		writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid", "read body")
		return
	}
	var req adminAllowRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid", "invalid json")
		return
	}
	backendID := strings.TrimSpace(req.Backend)
	model := strings.TrimSpace(req.Model)
	if backendID == "" || model == "" {
		writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid", "backend and model required")
		return
	}
	reg := h.router.Registry()
	b, ok := reg.Backend(backendID)
	if !ok {
		writeAdminJSONError(w, http.StatusNotFound, "invalid_request_error", "not_found", "backend not found")
		return
	}
	if b.Models == nil || !b.Models.HasAllowGate() {
		writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "failed_precondition", "backend has no models.allow gate")
		return
	}
	down := config.NormalizeDownstream(backendID, model)
	if down == "" {
		writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid", "invalid model")
		return
	}
	if i := strings.Index(model, "/"); i > 0 {
		prefix := model[:i]
		if prefix != backendID {
			if _, ok := reg.Config().Backends[prefix]; ok {
				writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "invalid", "model backend prefix mismatch")
				return
			}
		}
	}
	publicID := prefixModelID(backendID, down)
	if b.IsModelAllowed(model) || b.IsModelAllowed(down) || b.IsModelAllowed(publicID) {
		writeAdminAllowOK(w, backendID, down, publicID, true, b.Models.Allow)
		return
	}
	inventory := modelsHandler(h).inventoryForBackend(r, b)
	if !modelInInventory(backendID, down, model, inventory) {
		if len(inventory) == 0 {
			writeAdminJSONError(w, http.StatusServiceUnavailable, "invalid_request_error", "unavailable", "could not fetch backend inventory")
			return
		}
		writeAdminJSONError(w, http.StatusBadRequest, "invalid_request_error", "model_not_in_inventory", "model not in backend inventory")
		return
	}
	if err := config.OverlayAdd(h.overlayPath, backendID, down); err != nil {
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, "gateway.serveModelsAllow", "overlay"))
		return
	}
	if err := reg.AllowModel(backendID, down); err != nil {
		if rbErr := config.OverlayRemove(h.overlayPath, backendID, down); rbErr != nil {
			slog.Warn("allow overlay rollback failed after AllowModel error",
				"backend", backendID, "model", down, "rollback_err", rbErr, "allow_err", err)
		}
		writeHandlerError(w, derrors.Wrap(err, derrors.CodeInternal, "gateway.serveModelsAllow", "allow model"))
		return
	}
	b2, ok := reg.Backend(backendID)
	if !ok || b2.Models == nil {
		writeAdminAllowOK(w, backendID, down, publicID, false, nil)
		return
	}
	writeAdminAllowOK(w, backendID, down, publicID, false, b2.Models.Allow)
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

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if len(header) < 7 || !strings.EqualFold(header[:6], "bearer") {
		return ""
	}
	return strings.TrimSpace(header[6:])
}

func writeAdminUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="polypus-admin"`)
	writeAdminJSONError(w, http.StatusUnauthorized, "invalid_request_error", "unauthorized", "admin API key required")
}

func writeAdminJSONError(w http.ResponseWriter, status int, typ, code, msg string) {
	writeAdminJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    typ,
			"code":    code,
		},
	})
}

func writeAdminAllowOK(w http.ResponseWriter, backend, down, publicID string, already bool, allow []string) {
	writeAdminJSON(w, http.StatusOK, adminAllowResponse{
		Backend:        backend,
		Model:          down,
		PublicID:       publicID,
		AlreadyAllowed: already,
		Allow:          append([]string(nil), allow...),
	})
}

func writeAdminJSON(w http.ResponseWriter, status int, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
