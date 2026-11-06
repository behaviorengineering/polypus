package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// countingModels is an OpenAI-compatible backend whose model list the test can
// change, counting every list call.
type countingModels struct {
	srv   *httptest.Server
	calls atomic.Int32
	mu    sync.Mutex
	ids   []string
}

func newCountingModels(t *testing.T, ids ...string) *countingModels {
	t.Helper()
	m := &countingModels{ids: ids}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		m.calls.Add(1)
		m.mu.Lock()
		data := make([]map[string]any, 0, len(m.ids))
		for _, id := range m.ids {
			data = append(data, map[string]any{"id": id, "object": "model"})
		}
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *countingModels) setIDs(ids ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ids = ids
}

// newCacheFirstGateway serves the gated backend "cf" (allow: @cf/a) on m.
func newCacheFirstGateway(t *testing.T, m *countingModels) (http.Handler, *gatewayHandler) {
	t.Helper()
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig(m.srv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, &fakeRouter{reg: reg}, WithAdminStateDir(t.TempDir()))
	gw, ok := gatewayFromHandler(h)
	if !ok {
		t.Fatalf("unexpected handler %T", h)
	}
	return h, gw
}

func listModelIDs(t *testing.T, h http.Handler, query string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models%s: status %d body %s", query, rec.Code, rec.Body.String())
	}
	var list openaiModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	return ids
}

func TestModelsListServesFromCache(t *testing.T) {
	m := newCountingModels(t, "@cf/a", "@cf/b")
	h, gw := newCacheFirstGateway(t, m)
	modelsHandler{gw.shared}.warmInventory(context.Background(), false)
	warmed := m.calls.Load()
	if warmed == 0 {
		t.Fatal("warm-up must fetch the backend's list")
	}
	for range 2 {
		if ids := listModelIDs(t, h, ""); !slices.Contains(ids, "cf/@cf/a") {
			t.Fatalf("enabled list %v", ids)
		}
	}
	if ids := listModelIDs(t, h, "?view=inventory"); !slices.Contains(ids, "cf/@cf/b") {
		t.Fatalf("inventory view %v", ids)
	}
	if got := m.calls.Load(); got != warmed {
		t.Fatalf("GET /v1/models after warm-up made %d upstream calls, want 0", got-warmed)
	}
}

func TestModelsListColdBackendFillsCache(t *testing.T) {
	m := newCountingModels(t, "@cf/a")
	h, _ := newCacheFirstGateway(t, m)
	if ids := listModelIDs(t, h, ""); !slices.Contains(ids, "cf/@cf/a") {
		t.Fatalf("enabled list %v", ids)
	}
	if got := m.calls.Load(); got != 1 {
		t.Fatalf("a cold backend is fetched once, got %d calls", got)
	}
	listModelIDs(t, h, "")
	if got := m.calls.Load(); got != 1 {
		t.Fatalf("the second list must come from the cache, got %d calls", got)
	}
}

func TestAdminAllowRefreshesBackendCache(t *testing.T) {
	m := newCountingModels(t, "@cf/a")
	h, _ := newCacheFirstGateway(t, m)
	listModelIDs(t, h, "") // Fills the cache before @cf/b exists upstream.
	m.setIDs("@cf/a", "@cf/b")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, adminAllowJSONPost(`{"backend":"cf","model":"@cf/b"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("allow: status %d body %s", rec.Code, rec.Body.String())
	}
	before := m.calls.Load()
	if ids := listModelIDs(t, h, ""); !slices.Contains(ids, "cf/@cf/b") {
		t.Fatalf("the allowed model must list at once, without a refresh tick: %v", ids)
	}
	if got := m.calls.Load(); got != before {
		t.Fatalf("the list after allow must come from the refreshed cache, got %d upstream calls", got-before)
	}
}

func TestPeriodicRefreshUpdatesCache(t *testing.T) {
	m := newCountingModels(t, "@cf/a")
	h, gw := newCacheFirstGateway(t, m)
	listModelIDs(t, h, "?view=inventory")
	m.setIDs("@cf/a", "@cf/new")

	tick := make(chan time.Time)
	refreshed := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runModelsRefreshLoop(ctx, time.Minute,
			func(time.Duration) (<-chan time.Time, func()) { return tick, func() {} },
			func(iterCtx context.Context) {
				modelsHandler{gw.shared}.warmInventory(iterCtx, true)
				refreshed <- struct{}{}
			})
	}()
	tick <- time.Now() // One interval passes.
	<-refreshed
	cancel()
	<-done

	before := m.calls.Load()
	if ids := listModelIDs(t, h, "?view=inventory"); !slices.Contains(ids, "cf/@cf/new") {
		t.Fatalf("the refresh must update the cache: %v", ids)
	}
	if got := m.calls.Load(); got != before {
		t.Fatalf("the list after a refresh must come from the cache, got %d upstream calls", got-before)
	}
}
