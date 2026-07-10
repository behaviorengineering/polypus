package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

const modelsListTimeout = 5 * time.Second
const modelsMaxBody = 4 << 20
const modelsInventoryCacheTTL = time.Hour

// openaiModel is the OpenAI Models API object (list + retrieve).
type openaiModel struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type openaiModelList struct {
	Object string        `json:"object"`
	Data   []openaiModel `json:"data"`
}

type modelsInventoryCache struct {
	mu      sync.Mutex
	byKey   map[string]modelsCacheEntry
	ttl     time.Duration
	path    string
}

type modelsCacheEntry struct {
	At     time.Time     `json:"at"`
	Models []openaiModel `json:"models"`
}

func newModelsInventoryCache() *modelsInventoryCache {
	ttl := modelsInventoryCacheTTL
	if raw := strings.TrimSpace(os.Getenv("POLYPUS_MODELS_CACHE_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	path := strings.TrimSpace(os.Getenv("POLYPUS_MODELS_CACHE"))
	if path == "" {
		root := strings.TrimSpace(os.Getenv("POLYPUS_ROOT"))
		if root == "" {
			root = "."
		}
		path = filepath.Join(root, ".polypus", "models-inventory.json")
	}
	c := &modelsInventoryCache{
		byKey: make(map[string]modelsCacheEntry),
		ttl:   ttl,
		path:  path,
	}
	c.loadDisk()
	return c
}

func (c *modelsInventoryCache) loadDisk() {
	if c == nil || c.path == "" {
		return
	}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var disk map[string]modelsCacheEntry
	if err := json.Unmarshal(raw, &disk); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range disk {
		c.byKey[k] = v
	}
}

func (c *modelsInventoryCache) persist() {
	if c == nil || c.path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return
	}
	raw, err := json.MarshalIndent(c.byKey, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(c.path, raw, 0o644)
}

func (c *modelsInventoryCache) get(key string) ([]openaiModel, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[key]
	if !ok || len(e.Models) == 0 {
		return nil, false
	}
	if time.Since(e.At) > c.ttl {
		return nil, false
	}
	out := make([]openaiModel, len(e.Models))
	copy(out, e.Models)
	return out, true
}

func (c *modelsInventoryCache) put(key string, models []openaiModel) {
	if c == nil || len(models) == 0 {
		return
	}
	c.mu.Lock()
	c.byKey[key] = modelsCacheEntry{At: time.Now(), Models: append([]openaiModel(nil), models...)}
	c.mu.Unlock()
	c.persist()
}

func inventoryView(r *http.Request) bool {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if v == "inventory" {
		return true
	}
	return r.URL.Query().Get("inventory") == "1"
}

func (h *Handler) serveModelsList(w http.ResponseWriter, r *http.Request) {
	models := h.collectModels(r, inventoryView(r))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(openaiModelList{
		Object: "list",
		Data:   models,
	})
}

func (h *Handler) serveModelRetrieve(w http.ResponseWriter, r *http.Request) {
	rawID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	rawID = strings.Trim(rawID, "/")
	if rawID == "" {
		http.NotFound(w, r)
		return
	}
	id, err := url.PathUnescape(rawID)
	if err != nil {
		http.Error(w, "invalid model id", http.StatusBadRequest)
		return
	}
	id = strings.TrimSpace(id)
	// Retrieve is tool-facing unless inventory view is requested.
	for _, m := range h.collectModels(r, inventoryView(r)) {
		if m.ID == id {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(m)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("The model `%s` does not exist", id),
			"type":    "invalid_request_error",
			"param":   "model",
			"code":    "model_not_found",
		},
	})
}

func (h *Handler) collectModels(r *http.Request, asInventory bool) []openaiModel {
	reg := h.router.Registry()
	cfg := reg.Config()
	ids := cfg.BackendIDs()

	type result struct {
		backend string
		models  []openaiModel
	}
	results := make([]result, len(ids))
	var wg sync.WaitGroup
	for i, backendID := range ids {
		b, ok := cfg.Backends[backendID]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, backendID string, b config.BackendDef) {
			defer wg.Done()
			results[i] = result{backend: backendID, models: h.inventoryForBackend(r, b)}
		}(i, backendID, b)
	}
	wg.Wait()

	byID := make(map[string]openaiModel)
	for _, res := range results {
		b := cfg.Backends[res.backend]
		for _, m := range res.models {
			if !asInventory && !b.IsModelAllowed(m.ID) {
				continue
			}
			byID[m.ID] = m
		}
		// Bare default-backend form for allowed models only when not inventory-restricted... keep bare for allowed.
		if isDefaultBackend(cfg, res.backend) {
			for _, m := range res.models {
				if !asInventory && !b.IsModelAllowed(m.ID) {
					continue
				}
				down := config.NormalizeDownstream(res.backend, m.ID)
				if down == "" || down == m.ID {
					continue
				}
				if _, ok := byID[down]; !ok {
					byID[down] = openaiModel{
						ID:      down,
						Object:  "model",
						Created: m.Created,
						OwnedBy: firstNonEmpty(m.OwnedBy, res.backend),
					}
				}
			}
		}
	}

	// Env seeds for speech defaults (respect allow when set).
	for _, seed := range seedModelsFromEnv(cfg) {
		b, ok := cfg.Backends[seed.OwnedBy]
		if !ok {
			// OwnedBy may be backend; for bare seed owned_by is backend
			for _, id := range ids {
				if strings.HasPrefix(seed.ID, id+"/") || seed.OwnedBy == id {
					b = cfg.Backends[id]
					ok = true
					break
				}
			}
		}
		if ok && !asInventory && !b.IsModelAllowed(seed.ID) {
			continue
		}
		if _, exists := byID[seed.ID]; !exists {
			byID[seed.ID] = seed
		}
	}

	out := make([]openaiModel, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (h *Handler) inventoryForBackend(r *http.Request, b config.BackendDef) []openaiModel {
	modelsCfg := b.Models
	if modelsCfg != nil && !modelsCfg.ShouldSync() {
		return syntheticModels(b.ID, modelsCfg.Allow)
	}
	live := fetchBackendModels(r, b.BaseURL, b.ID)
	if len(live) > 0 {
		if h.invCache != nil {
			h.invCache.put(b.ID, live)
		}
		return live
	}
	if h.invCache != nil {
		if cached, ok := h.invCache.get(b.ID); ok {
			return cached
		}
	}
	// No catalog: seeds + optional synthetic allow when gated.
	out := seedModelsForBackend(h.router.Registry().Config(), b.ID)
	if len(out) == 0 && modelsCfg != nil && modelsCfg.HasAllowGate() {
		return syntheticModels(b.ID, modelsCfg.Allow)
	}
	return out
}

func syntheticModels(backendID string, allow []string) []openaiModel {
	ids := config.SyntheticAllowModels(backendID, allow)
	out := make([]openaiModel, 0, len(ids))
	for _, id := range ids {
		out = append(out, openaiModel{
			ID:      id,
			Object:  "model",
			Created: 0,
			OwnedBy: backendID,
		})
	}
	return out
}

func fetchBackendModels(r *http.Request, baseURL, backendID string) []openaiModel {
	target := openAIModelsURL(baseURL)
	ctx := r.Context()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	client := &http.Client{Timeout: modelsListTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return nil
	}
	return rewriteBackendModelList(raw, backendID)
}

func rewriteBackendModelList(raw []byte, backendID string) []openaiModel {
	var list openaiModelList
	if err := json.Unmarshal(raw, &list); err == nil && (list.Object == "list" || len(list.Data) > 0) {
		out := make([]openaiModel, 0, len(list.Data))
		for _, m := range list.Data {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				continue
			}
			out = append(out, openaiModel{
				ID:      prefixModelID(backendID, id),
				Object:  "model",
				Created: m.Created,
				OwnedBy: firstNonEmpty(m.OwnedBy, backendID),
			})
		}
		return out
	}
	var arr []openaiModel
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		out := make([]openaiModel, 0, len(arr))
		for _, m := range arr {
			id := strings.TrimSpace(m.ID)
			if id == "" {
				continue
			}
			out = append(out, openaiModel{
				ID:      prefixModelID(backendID, id),
				Object:  "model",
				Created: m.Created,
				OwnedBy: firstNonEmpty(m.OwnedBy, backendID),
			})
		}
		return out
	}
	return nil
}

// openAIModelsURL joins base (with or without trailing /v1) to the OpenAI models path.
func openAIModelsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/models"
	}
	return base + "/v1/models"
}

func prefixModelID(backendID, modelID string) string {
	backendID = strings.TrimSpace(backendID)
	modelID = strings.TrimSpace(modelID)
	if backendID == "" {
		return modelID
	}
	if strings.HasPrefix(modelID, backendID+"/") {
		return modelID
	}
	return backendID + "/" + modelID
}

func seedModelsFromEnv(cfg config.RouterConfig) []openaiModel {
	var out []openaiModel
	seen := make(map[string]struct{})
	add := func(backendID, modelID string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" || backendID == "" {
			return
		}
		id := prefixModelID(backendID, modelID)
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, openaiModel{
			ID:      id,
			Object:  "model",
			Created: 0,
			OwnedBy: backendID,
		})
		if isDefaultBackend(cfg, backendID) {
			if _, ok := seen[modelID]; !ok {
				seen[modelID] = struct{}{}
				out = append(out, openaiModel{
					ID:      modelID,
					Object:  "model",
					Created: 0,
					OwnedBy: backendID,
				})
			}
		}
	}
	add(cfg.DefaultTTSBackend, os.Getenv("POLYPUS_DEFAULT_MODEL"))
	add(cfg.DefaultSTTBackend, os.Getenv("POLYPUS_DEFAULT_STT_MODEL"))
	return out
}

func seedModelsForBackend(cfg config.RouterConfig, backendID string) []openaiModel {
	var out []openaiModel
	for _, m := range seedModelsFromEnv(cfg) {
		if m.OwnedBy == backendID || strings.HasPrefix(m.ID, backendID+"/") {
			out = append(out, m)
		}
	}
	return out
}

func isDefaultBackend(cfg config.RouterConfig, backendID string) bool {
	return backendID == cfg.DefaultTTSBackend ||
		backendID == cfg.DefaultSTTBackend ||
		backendID == cfg.DefaultChatBackend ||
		backendID == cfg.DefaultVisionBackend ||
		backendID == cfg.DefaultEmbedBackend
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func writeModelNotAllowed(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": fmt.Sprintf("The model `%s` is not enabled on this gateway (models.allow)", model),
			"type":    "invalid_request_error",
			"param":   "model",
			"code":    "model_not_allowed",
		},
	})
}

func (h *Handler) ensureModelAllowed(backendID, model string) bool {
	reg := h.router.Registry()
	b, ok := reg.Config().Backends[backendID]
	if !ok {
		return true
	}
	return b.IsModelAllowed(model)
}
