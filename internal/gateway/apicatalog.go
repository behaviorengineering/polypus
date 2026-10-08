package gateway

import (
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

// ModelSurface is a gateway API flavor for model catalog partitioning.
type ModelSurface int

const (
	surfaceOpenAI ModelSurface = iota
	surfaceSystemOne
)

const openAIModelsCanonicalPath = "/v1/apis/openai/models"

// apiOperation describes one HTTP operation for discovery clients.
type apiOperation struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// apiCatalogEntry is one discoverable API surface on the gateway.
type apiCatalogEntry struct {
	ID         string                  `json:"id"`
	Title      string                  `json:"title"`
	Flavor     string                  `json:"flavor"`
	Models     string                  `json:"models"`
	Schema     string                  `json:"schema"`
	Operations map[string]apiOperation `json:"operations"`
}

// apiCatalog is the top-level GET /v1/apis response.
type apiCatalog struct {
	Object string            `json:"object"`
	APIs   []apiCatalogEntry `json:"apis"`
}

func buildAPICatalog(cfg config.RouterConfig) apiCatalog {
	entries := []apiCatalogEntry{openAICatalogEntry()}
	if cfg.EffectiveSystemOneBackend() != "" {
		entries = append(entries, systemOneCatalogEntry())
	}
	return apiCatalog{
		Object: "api_catalog",
		APIs:   entries,
	}
}

func openAICatalogEntry() apiCatalogEntry {
	return apiCatalogEntry{
		ID:     "openai",
		Title:  "OpenAI-compatible",
		Flavor: "openai",
		Models: "/v1/apis/openai/models",
		Schema: "/v1/apis/openai/openapi.yaml",
		Operations: map[string]apiOperation{
			"list_models":          {Method: "GET", Path: "/v1/apis/openai/models"},
			"retrieve_model":       {Method: "GET", Path: "/v1/apis/openai/models/{id}"},
			"chat_completions":     {Method: "POST", Path: "/v1/chat/completions"},
			"embeddings":           {Method: "POST", Path: "/v1/embeddings"},
			"audio_speech":         {Method: "POST", Path: "/v1/audio/speech"},
			"audio_transcriptions": {Method: "POST", Path: "/v1/audio/transcriptions"},
			"audio_voices":         {Method: "GET", Path: "/v1/audio/voices"},
			"files":                {Method: "GET", Path: "/v1/files"},
			"batches":              {Method: "POST", Path: "/v1/batches"},
		},
	}
}

func systemOneCatalogEntry() apiCatalogEntry {
	return apiCatalogEntry{
		ID:     "systemone",
		Title:  "SystemOne",
		Flavor: "systemone",
		Models: "/v1/apis/systemone/models",
		Schema: "/v1/apis/systemone/schema.json",
		Operations: map[string]apiOperation{
			"list_models":    {Method: "GET", Path: "/v1/apis/systemone/models"},
			"retrieve_model": {Method: "GET", Path: "/v1/apis/systemone/models/{id}"},
			"evaluate":       {Method: "POST", Path: "/v1/systemone"},
		},
	}
}

type apiCatalogHandler struct{ *shared }

func (h apiCatalogHandler) serveCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := config.RouterConfig{}
	if h.router != nil {
		cfg = h.router.Registry().Config()
	}
	cat := buildAPICatalog(cfg)
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	encodeJSON(w, cat)
}

func setOpenAIModelsCanonicalLink(w http.ResponseWriter) {
	if w == nil {
		return
	}
	w.Header().Set("Link", "<"+openAIModelsCanonicalPath+">; rel=\"canonical\"")
}

// isSystemOneListedModel reports whether publicID belongs on the SystemOne model surface.
func isSystemOneListedModel(cfg config.RouterConfig, publicID string) bool {
	publicID = strings.TrimSpace(publicID)
	if publicID == "" {
		return false
	}
	if cfg.EffectiveSystemOneBackend() == "" {
		return false
	}
	backendID, down := backendAndDownstreamForPublicID(cfg, publicID)
	if backendID == "" {
		return false
	}
	b, ok := cfg.Backends[backendID]
	if !ok {
		return false
	}
	return b.IsSystemOneDownstream(down)
}

func backendAndDownstreamForPublicID(cfg config.RouterConfig, publicID string) (backendID, downstream string) {
	publicID = strings.TrimSpace(publicID)
	if i := strings.Index(publicID, "/"); i > 0 {
		prefix := publicID[:i]
		if _, ok := cfg.Backends[prefix]; ok {
			return prefix, config.NormalizeDownstream(prefix, publicID)
		}
	}
	def := cfg.EffectiveSystemOneBackend()
	if def != "" {
		return def, config.NormalizeDownstream(def, publicID)
	}
	for id, b := range cfg.Backends {
		if b.HasCapability(config.CapSystemOne) {
			return id, config.NormalizeDownstream(id, publicID)
		}
	}
	return "", publicID
}

func partitionModels(cfg config.RouterConfig, all []openaiModel, surface ModelSurface) []openaiModel {
	out := make([]openaiModel, 0, len(all))
	for _, m := range all {
		sys := isSystemOneListedModel(cfg, m.ID)
		switch surface {
		case surfaceOpenAI:
			if !sys {
				out = append(out, m)
			}
		case surfaceSystemOne:
			if sys {
				out = append(out, m)
			}
		}
	}
	return out
}

func modelsForSurface(cfg config.RouterConfig, all []openaiModel, surface ModelSurface) []openaiModel {
	return partitionModels(cfg, all, surface)
}

func systemOneAPIEnabled(cfg config.RouterConfig) bool {
	return cfg.EffectiveSystemOneBackend() != ""
}
