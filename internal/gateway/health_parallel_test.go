package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

// probeBarrier answers 200 only once every backend sharing the barrier has been
// probed; a serial probe loop would leave the first one waiting until it gives up.
type probeBarrier struct {
	mu      sync.Mutex
	arrived int
	want    int
	all     chan struct{}
}

func newProbeBarrier(want int) *probeBarrier {
	return &probeBarrier{want: want, all: make(chan struct{})}
}

func (b *probeBarrier) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.arrived++
		if b.arrived == b.want {
			close(b.all)
		}
		b.mu.Unlock()
		select {
		case <-b.all:
			w.WriteHeader(http.StatusOK)
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusServiceUnavailable)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func twoLocalBackendsConfig(t *testing.T, urlA, urlB string) {
	t.Helper()
	writeConfig(t, t.TempDir(), fmt.Sprintf(`
chat_backend:
  enabled: true
  default: slow_a
backends:
  slow_a:
    base_url: %s
    capabilities: [chat]
  slow_b:
    base_url: %s
    capabilities: [chat]
`, urlA, urlB))
}

func TestHealthBackendsProbesInParallel(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	barrier := newProbeBarrier(2)
	a, b := barrier.server(t), barrier.server(t)
	twoLocalBackendsConfig(t, a.URL, b.URL)
	handler, err := NewHandler(config.ServeOptions{BackendURL: a.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/backends", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("both slow probes must finish within one budget: status %d body %s", rec.Code, rec.Body.String())
	}
	var resp backendHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Backends) != 2 || resp.Backends[0].ID != "slow_a" || resp.Backends[1].ID != "slow_b" {
		t.Fatalf("backends must keep config order: %+v", resp.Backends)
	}
}

func TestHealthBackendsBodyHasNoCauseChain(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)
	deadURL := dead.URL
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(up.Close)
	twoLocalBackendsConfig(t, up.URL, deadURL)
	handler, err := NewHandler(config.ServeOptions{BackendURL: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/backends", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp backendHealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var failed *backendProbeResult
	for i := range resp.Backends {
		if resp.Backends[i].ID == "slow_b" {
			failed = &resp.Backends[i]
		}
	}
	if failed == nil || failed.OK || failed.Code == "" || failed.Error == "" {
		t.Fatalf("failed probe entry: %+v", failed)
	}
	for _, leak := range []string{"gateway.probeBackend", "outbound", strings.TrimPrefix(deadURL, "http://")} {
		if strings.Contains(failed.Error, leak) {
			t.Fatalf("probe error leaks %q: %q", leak, failed.Error)
		}
	}
}
