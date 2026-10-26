package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/statefile"

	"github.com/behaviorengineering/polypus/internal/clients/openaicompat"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
)

const (
	modelsListTimeout       = 5 * time.Second
	modelsMaxBody           = 4 << 20
	modelsInventoryCacheTTL = time.Hour
)

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
	mu    sync.Mutex
	byKey map[string]modelsCacheEntry
	dirty bool // put since the last flush.
	ttl   time.Duration
	path  string
	now   func() time.Time
	log   *slog.Logger
	// writeMu orders disk writes, so an older snapshot never lands after a newer one.
	writeMu sync.Mutex
}

type modelsCacheEntry struct {
	At     time.Time     `json:"at"`
	Models []openaiModel `json:"models"`
}

func newModelsInventoryCache(log *slog.Logger) *modelsInventoryCache {
	ttl := modelsInventoryCacheTTL
	if raw := strings.TrimSpace(os.Getenv("POLYPUS_MODELS_CACHE_TTL")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			ttl = d
		}
	}
	path, err := config.ResolveModelsInventoryCachePath()
	if err != nil {
		// The disk cache is optional: run memory-only and say why.
		log.Warn("polypus: models inventory disk cache disabled", "err", err)
		path = ""
	}
	c := &modelsInventoryCache{
		log:   log,
		byKey: make(map[string]modelsCacheEntry),
		ttl:   ttl,
		path:  path,
		now:   time.Now,
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
		if !errors.Is(err, fs.ErrNotExist) {
			c.log.Warn("polypus: models inventory cache unreadable; starting empty", "path", c.path, "err", err)
		}
		return
	}
	var disk map[string]modelsCacheEntry
	if err := json.Unmarshal(raw, &disk); err != nil {
		c.log.Warn("polypus: models inventory cache corrupt; starting empty", "path", c.path, "err", err)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range disk {
		c.byKey[k] = v
	}
}

// flush writes the cache to disk when something was put since the last flush. It
// marshals a snapshot under the lock and writes outside it, so lookups are not
// held up by disk I/O. The cache is best effort: a failed write is logged and the
// in-memory entries stay.
func (c *modelsInventoryCache) flush() {
	if c == nil || c.path == "" {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return
	}
	raw, err := json.MarshalIndent(c.byKey, "", "  ")
	c.dirty = false
	c.mu.Unlock()
	if err == nil {
		err = statefile.WriteAtomic(c.path, raw, 0o600)
	}
	if err != nil {
		c.log.Warn("polypus: models inventory cache not saved", "path", c.path, "err", err)
	}
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
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if now().Sub(e.At) > c.ttl {
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
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	c.mu.Lock()
	c.byKey[key] = modelsCacheEntry{At: now(), Models: append([]openaiModel(nil), models...)}
	c.dirty = true
	c.mu.Unlock()
}

func inventoryView(r *http.Request) bool {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
	if v == "inventory" {
		return true
	}
	return r.URL.Query().Get("inventory") == "1"
}

func (h modelsHandler) serveModelsList(w http.ResponseWriter, r *http.Request) {
	h.serveModelsListForSurface(surfaceOpenAI, w, r)
}

func (h modelsHandler) serveModelsListForSurface(surface ModelSurface, w http.ResponseWriter, r *http.Request) {
	cfg := config.RouterConfig{}
	if h.router != nil {
		cfg = h.router.Registry().Config()
	}
	if surface == surfaceSystemOne && !systemOneAPIEnabled(cfg) {
		h.writeAPINotConfigured(w)
		return
	}
	all := h.collectModels(r, inventoryView(r))
	models := modelsForSurface(cfg, all, surface)
	if surface == surfaceOpenAI {
		setOpenAIModelsCanonicalLink(w)
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	h.encodeJSON(w, openaiModelList{
		Object: objectList,
		Data:   models,
	})
}

func (s *shared) writeAPINotConfigured(w http.ResponseWriter) {
	s.writeOpenAIJSON(w, http.StatusNotFound, openaiErrorBody{Error: openaiError{
		Message: "SystemOne API is not configured on this gateway",
		Type:    errTypeInvalidRequest,
		Code:    "api_not_configured",
	}})
}

func (h modelsHandler) serveModelRetrieve(w http.ResponseWriter, r *http.Request) {
	h.serveModelRetrieveForSurface(surfaceOpenAI, "/v1/models/", w, r)
}

func (h modelsHandler) serveModelRetrieveForSurface(surface ModelSurface, pathPrefix string, w http.ResponseWriter, r *http.Request) {
	cfg := config.RouterConfig{}
	if h.router != nil {
		cfg = h.router.Registry().Config()
	}
	if surface == surfaceSystemOne && !systemOneAPIEnabled(cfg) {
		h.writeAPINotConfigured(w)
		return
	}
	rawID := strings.TrimPrefix(r.URL.Path, pathPrefix)
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
	asInventory := inventoryView(r)
	all := h.collectModelsForRetrieve(r, asInventory, id)
	if m, ok := lookupModelForSurface(cfg, all, surface, id); ok {
		if surface == surfaceOpenAI {
			setOpenAIModelsCanonicalLink(w)
		}
		w.Header().Set("Content-Type", "application/json")
		h.encodeJSON(w, m)
		return
	}
	h.writeOpenAIJSON(w, http.StatusNotFound, openaiErrorBody{Error: openaiError{
		Message: fmt.Sprintf("The model `%s` does not exist", id),
		Type:    errTypeInvalidRequest,
		Param:   keyModel,
		Code:    "model_not_found",
	}})
}

// collectModelsForRetrieve loads catalog data for a single model id when not in inventory view.
func (h modelsHandler) collectModelsForRetrieve(r *http.Request, asInventory bool, targetID string) []openaiModel {
	if asInventory || h.router == nil {
		return h.collectModels(r, asInventory)
	}
	targetID = strings.TrimSpace(targetID)
	reg := h.router.Registry()
	cfg := reg.Config()
	catalog := routerCatalogModels(cfg)
	for _, m := range catalog {
		if m.ID == targetID {
			return catalog
		}
	}
	backendID, _ := backendAndDownstreamForPublicID(cfg, targetID)
	if backendID == "" {
		return h.collectModels(r, false)
	}
	b, ok := cfg.Backends[backendID]
	if !ok {
		return h.collectModels(r, false)
	}
	r, cancel := withModelsBudget(r, cfg)
	defer cancel()
	results := []backendInventory{{backend: backendID, models: h.inventoryForBackend(r, b)}}
	h.invCache.flush()
	byID := mergeBackendInventories(cfg, false, results)
	mergeSeedModels(cfg, false, []string{backendID}, byID, seedModelsFromEnv(cfg))
	return finalizeModelList(catalog, byID)
}

// tracedProbeClient carries the model-list and health probes: no client Timeout
// (the caller's ctx deadline bounds each call) and a client span per request.
var tracedProbeClient = &http.Client{Transport: observability.WrapTransport(http.DefaultTransport)}

// withModelsBudget bounds one models pass: the gateway sets the budget here, once,
// so the catalog hops below never invent one. A sooner caller deadline still wins.
func withModelsBudget(r *http.Request, cfg config.RouterConfig) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), modelsWarmupTimeout(cfg))
	return r.WithContext(ctx), cancel
}

func (h modelsHandler) collectModels(r *http.Request, asInventory bool) []openaiModel {
	// Callers fall back here when there is no router, so this is the one guard.
	if h.shared == nil || h.router == nil {
		return nil
	}
	r, cancel := withModelsBudget(r, h.router.Registry().Config())
	defer cancel()
	reg := h.router.Registry()
	cfg := reg.Config()
	ids := cfg.BackendIDs()

	var catalog []openaiModel
	if !asInventory {
		catalog = routerCatalogModels(cfg)
	}

	results := make([]backendInventory, len(ids))
	var wg sync.WaitGroup
	for i, backendID := range ids {
		b, ok := cfg.Backends[backendID]
		if !ok {
			continue
		}
		wg.Add(1)
		go func(i int, backendID string, b config.BackendDef) {
			defer wg.Done()
			results[i] = backendInventory{backend: backendID, models: h.inventoryForBackend(r, b)}
		}(i, backendID, b)
	}
	wg.Wait()
	// One disk write per pass, not one per backend.
	h.invCache.flush()

	byID := mergeBackendInventories(cfg, asInventory, results)
	mergeSeedModels(cfg, asInventory, ids, byID, seedModelsFromEnv(cfg))
	return finalizeModelList(catalog, byID)
}

func (h modelsHandler) inventoryForBackend(r *http.Request, b config.BackendDef) []openaiModel {
	syncOff := b.Models != nil && !b.Models.ShouldSync()
	if list := h.liveCatalogLister(b); list != nil {
		if syncOff {
			return mergeEnabledSyntheticMissing(b, nil)
		}
		return h.liveOrCached(b, h.catalogInventory(r, b, list), nil)
	}
	if b.IsCloudflareExtension() && !syncOff {
		return h.liveOrCached(b, h.cloudflareInventory(r, b), nil)
	}
	if syncOff {
		return mergeEnabledSyntheticMissing(b, nil)
	}
	live := h.fetchBackendModelsProtected(r, b)
	if isOpenRouterHost(b.BaseURL) {
		live = h.mergeOpenRouterDecisionModels(r, b, live)
	}
	// No catalog and no cache: the configured seeds.
	return h.liveOrCached(b, live, seedModelsForBackend(h.router.Registry().Config(), b.ID))
}

// liveOrCached returns the live list (and caches it), else the last cached list,
// else fallback. Live and fallback lists gain the enabled synthetic models.
func (h modelsHandler) liveOrCached(b config.BackendDef, live, fallback []openaiModel) []openaiModel {
	if len(live) > 0 {
		live = mergeEnabledSyntheticMissing(b, live)
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
	return mergeEnabledSyntheticMissing(b, fallback)
}

func syntheticModels(backendID string, allow []string) []openaiModel {
	ids := config.SyntheticAllowModels(backendID, allow)
	out := make([]openaiModel, 0, len(ids))
	for _, id := range ids {
		out = append(out, openaiModel{
			ID:      id,
			Object:  keyModel,
			Created: 0,
			OwnedBy: backendID,
		})
	}
	return out
}

// cloudflareInventory returns CF models for /v1/models. Client and list errors
// soft-fail to an empty slice (Warn only) so one backend outage does not fail the catalog.
func (h modelsHandler) cloudflareInventory(r *http.Request, b config.BackendDef) []openaiModel {
	var out []openaiModel
	if execErr := h.upstreams.Execute(b.ID, func() error {
		client, err := h.cloudflareClient(b)
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
				Object:  keyModel,
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

func (h modelsHandler) fetchBackendModelsProtected(r *http.Request, b config.BackendDef) []openaiModel {
	var out []openaiModel
	if execErr := h.upstreams.Execute(b.ID, func() error {
		models, err := h.fetchBackendModelsErr(r, b)
		if err != nil {
			return err
		}
		out = models
		return nil
	}); execErr != nil {
		h.log.Warn("polypus: models catalog fetch failed", "backend", b.ID, "err", execErr)
	}
	return out
}

func (s *shared) mergeOpenRouterDecisionModels(r *http.Request, b config.BackendDef, live []openaiModel) []openaiModel {
	decisions, err := s.fetchBackendModelsAtURL(r, b, openRouterModelsListURL(b.BaseURL, "decisions"))
	if err != nil || len(decisions) == 0 {
		return live
	}
	seen := make(map[string]struct{}, len(live)+len(decisions))
	out := append([]openaiModel(nil), live...)
	for _, m := range live {
		seen[m.ID] = struct{}{}
	}
	for _, m := range decisions {
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	return out
}

func openRouterModelsListURL(base string, modality string) string {
	target := openAIModelsListURL(base)
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	q.Set("output_modalities", modality)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *shared) fetchBackendModelsAtURL(r *http.Request, b config.BackendDef, target string) ([]openaiModel, error) {
	const op = "gateway.fetchBackendModels"
	// A remote backend gets its own credential or nothing: the caller's
	// Authorization header never leaves for a third-party provider.
	auth, err := bearerAuthHeader(b)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeNotReady, op, "backend auth").With("backend", b.ID)
	}
	if auth == "" && !b.Remote {
		auth = r.Header.Get("Authorization")
	}
	// r carries the models pass budget (withModelsBudget); the client fails closed
	// without a deadline.
	models, err := s.compat.ListModels(r.Context(), target, auth)
	if err != nil {
		return nil, gatewayErr(err, op, "list").With("backend", b.ID)
	}
	return publicModels(models, b.ID), nil
}

func (s *shared) fetchBackendModelsErr(r *http.Request, b config.BackendDef) ([]openaiModel, error) {
	return s.fetchBackendModelsAtURL(r, b, openAIModelsListURL(b.BaseURL))
}

// publicModels turns a backend's model list into public ids under the backend.
func publicModels(models []openaicompat.Model, backendID string) []openaiModel {
	out := make([]openaiModel, 0, len(models))
	for _, m := range models {
		out = append(out, openaiModel{
			ID:      prefixModelID(backendID, m.ID),
			Object:  keyModel,
			Created: m.Created,
			OwnedBy: firstNonEmpty(m.OwnedBy, backendID),
		})
	}
	return out
}

// isOpenAICompatAPIRoot reports whether base already ends at the OpenAI API root
// (/v1 or DeepInfra-style /v1/openai), so callers should join /models or
// /chat/completions without inserting another /v1.
func isOpenAICompatAPIRoot(base string) bool {
	b := strings.ToLower(strings.TrimRight(strings.TrimSpace(base), "/"))
	if strings.HasSuffix(b, "/v1/openai") {
		return true
	}
	return strings.HasSuffix(b, "/v1")
}

// openAIModelsURL joins base (with or without trailing /v1 or /v1/openai) to the OpenAI models path.
func openAIModelsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if isOpenAICompatAPIRoot(base) {
		return base + "/models"
	}
	return base + "/v1/models"
}

// isOpenRouterHost reports whether base URL targets OpenRouter's API host.
func isOpenRouterHost(base string) bool {
	base = strings.TrimSpace(base)
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

// isDeepInfraHost reports whether base URL targets DeepInfra's API host.
func isDeepInfraHost(base string) bool {
	base = strings.TrimSpace(base)
	if base == "" {
		return false
	}
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	return host == "deepinfra.com" || host == "api.deepinfra.com" || strings.HasSuffix(host, ".deepinfra.com")
}

// deepInfraModelsListURL is GET /v1/openai/models on the DeepInfra origin.
// Chat uses POST /v1/openai/chat/completions; inventory does not follow a
// chat base that omits /openai (https://api.deepinfra.com/v1 → /v1/models).
func deepInfraModelsListURL(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return openAIModelsURL(base)
	}
	u.Path = "/v1/openai/models"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// openAIModelsListURL is the upstream models list URL, including OpenRouter modality query
// and the DeepInfra OpenAI-compat catalog path.
func openAIModelsListURL(base string) string {
	if isDeepInfraHost(base) {
		return deepInfraModelsListURL(base)
	}
	target := openAIModelsURL(base)
	if !isOpenRouterHost(base) {
		return target
	}
	u, err := url.Parse(target)
	if err != nil {
		return target
	}
	q := u.Query()
	if q.Get("output_modalities") == "" {
		q.Set("output_modalities", "all")
		u.RawQuery = q.Encode()
	}
	return u.String()
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
			Object:  keyModel,
			Created: 0,
			OwnedBy: backendID,
		})
		if isDefaultBackend(cfg, backendID) {
			if _, ok := seen[modelID]; !ok {
				seen[modelID] = struct{}{}
				out = append(out, openaiModel{
					ID:      modelID,
					Object:  keyModel,
					Created: 0,
					OwnedBy: backendID,
				})
			}
		}
	}
	add(cfg.EffectiveTTSBackend(), cfg.DefaultTTSModel)
	add(cfg.EffectiveSTTBackend(), cfg.DefaultSTTModel)
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
	return backendID == cfg.EffectiveTTSBackend() ||
		backendID == cfg.EffectiveSTTBackend() ||
		backendID == cfg.EffectiveChatBackend() ||
		backendID == cfg.EffectiveVisionBackend() ||
		backendID == cfg.EffectiveEmbedBackend() ||
		backendID == cfg.EffectiveProxyBackend() ||
		backendID == cfg.EffectiveSystemOneBackend()
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func (s *shared) writeModelNotAllowed(w http.ResponseWriter, model string) {
	s.writeOpenAIJSON(w, http.StatusBadRequest, openaiErrorBody{Error: openaiError{
		Message: fmt.Sprintf("The model `%s` is not enabled on this gateway (models.allow)", model),
		Type:    errTypeInvalidRequest,
		Param:   keyModel,
		Code:    "model_not_allowed",
	}})
}

func (s *shared) ensureModelAllowed(backendID, model string) bool {
	if s == nil || s.router == nil {
		return true
	}
	reg := s.router.Registry()
	b, ok := reg.Config().Backends[backendID]
	if !ok {
		return true
	}
	return b.IsModelAllowed(model)
}

func (s *shared) ensureSystemOneAllowed(backendID, publicModel, downstream string) bool {
	if s == nil || s.router == nil {
		return true
	}
	b, ok := s.router.Registry().Config().Backends[backendID]
	if !ok {
		return true
	}
	if !b.HasCapability(config.CapSystemOne) {
		return s.ensureModelAllowed(backendID, publicModel)
	}
	down := strings.TrimSpace(downstream)
	if down == "" {
		down = config.NormalizeDownstream(backendID, publicModel)
	}
	return b.IsSystemOneDownstream(down)
}
