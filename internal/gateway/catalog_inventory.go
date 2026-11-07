package gateway

import (
	"context"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// strictLister is a provider catalog client.
type strictLister interface {
	ListModelsStrict(ctx context.Context) (catalog.ModelList, error)
}

// liveCatalogClient builds the provider catalog client for b (Gemini, Anthropic,
// Cloudflare). ok is false for a backend whose models do not come from a provider
// catalog. It runs before the backend's breaker sees the call, so a client that
// cannot be built (a missing credential) answers as itself and never trips it.
func (h modelsHandler) liveCatalogClient(b config.BackendDef) (client strictLister, ok bool, err error) {
	switch {
	case b.IsGeminiExtension():
		return catalogClient(h.clients.geminiClient, b)
	case b.IsAnthropicExtension():
		return catalogClient(h.clients.anthropicClient, b)
	case b.IsCloudflareExtension():
		return catalogClient(h.clients.cloudflareClient, b)
	default:
		return nil, false, nil
	}
}

// catalogClient builds b's catalog client through get.
func catalogClient[C strictLister](get func(config.BackendDef) (C, error), b config.BackendDef) (strictLister, bool, error) {
	client, err := get(b)
	if err != nil {
		return nil, true, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "gateway.catalogClient", "catalog client").
			With("backend", b.ID)
	}
	return client, true, nil
}

// catalogInventory returns a backend's catalog models for /v1/models. List errors
// soft-fail to an empty slice (Warn only) so one backend outage does not fail the
// catalog.
func (h modelsHandler) catalogInventory(ctx context.Context, b config.BackendDef, client strictLister) []openaiModel {
	var out []openaiModel
	if execErr := h.upstreams.Execute(ctx, b.ID, func() error {
		models, listErr := client.ListModelsStrict(ctx)
		if listErr != nil {
			return gatewayErr(listErr, "gateway.catalogInventory", "list models").With("backend", b.ID)
		}
		out = make([]openaiModel, 0, len(models.Data))
		for _, m := range models.Data {
			out = append(out, openaiModel{
				ID:      prefixModelID(b.ID, m.ID),
				Object:  objectModel,
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
