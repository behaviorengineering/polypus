package gateway

import (
	"context"
	"net/http"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
)

// catalogLister lists a backend's models from its provider catalog client.
type catalogLister func(context.Context) (catalog.ModelList, error)

// liveCatalogLister returns the catalog list call for backends whose models come from a
// provider catalog client (Gemini, Anthropic), or nil for every other backend.
func (h modelsHandler) liveCatalogLister(b config.BackendDef) catalogLister {
	switch {
	case b.IsGeminiExtension():
		return func(ctx context.Context) (catalog.ModelList, error) {
			client, err := h.geminiClient(b)
			if err != nil {
				return catalog.ModelList{}, err
			}
			return client.ListModelsStrict(ctx)
		}
	case b.IsAnthropicExtension():
		return func(ctx context.Context) (catalog.ModelList, error) {
			client, err := h.anthropicClient(b)
			if err != nil {
				return catalog.ModelList{}, err
			}
			return client.ListModelsStrict(ctx)
		}
	default:
		return nil
	}
}

// catalogInventory returns a backend's catalog models for /v1/models. Client and list errors
// soft-fail to an empty slice (Warn only) so one backend outage does not fail the catalog.
func (h modelsHandler) catalogInventory(r *http.Request, b config.BackendDef, list catalogLister) []openaiModel {
	var out []openaiModel
	if execErr := h.upstreams.Execute(b.ID, func() error {
		models, listErr := list(r.Context())
		if listErr != nil {
			return listErr
		}
		out = make([]openaiModel, 0, len(models.Data))
		for _, m := range models.Data {
			out = append(out, openaiModel{
				ID:      prefixModelID(b.ID, m.ID),
				Object:  catalog.ObjectModel,
				Created: m.Created,
				OwnedBy: firstNonEmpty(m.OwnedBy, b.ID),
			})
		}
		return nil
	}); execErr != nil {
		h.log.Warn("polypus: models catalog fetch failed", "backend", b.ID, "err", execErr)
	}
	return out
}
