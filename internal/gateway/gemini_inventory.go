package gateway

import (
	"log/slog"
	"net/http"

	"github.com/behaviorengineering/polypus/internal/config"
)

// geminiInventory returns Gemini models for /v1/models. Client and list errors
// soft-fail to an empty slice (Warn only) so one backend outage does not fail the catalog.
func (h modelsHandler) geminiInventory(r *http.Request, b config.BackendDef) []openaiModel {
	var out []openaiModel
	if execErr := h.upstreams.Execute(b.ID, func() error {
		client, err := h.geminiClient(b)
		if err != nil {
			return err
		}
		list, listErr := client.ListModelsStrict(r.Context())
		if listErr != nil {
			return listErr
		}
		out = make([]openaiModel, 0, len(list.Data))
		for _, m := range list.Data {
			out = append(out, openaiModel{
				ID:      prefixModelID(b.ID, m.ID),
				Object:  "model",
				Created: m.Created,
				OwnedBy: firstNonEmpty(m.OwnedBy, b.ID),
			})
		}
		return nil
	}); execErr != nil {
		slog.Warn("polypus: models catalog fetch failed", "backend", b.ID, "err", execErr)
	}
	return out
}
