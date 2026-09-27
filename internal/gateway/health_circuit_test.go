package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

// Backend health probes must not open the production circuit breaker; otherwise
// stack-doctor /health/backends can brick chat until the breaker times out.
func TestBackendHealthDoesNotOpenChatCircuit(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	n := 0
	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(leaf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
chat_backend:
  enabled: true
  default: leaf
tts_backend:
  enabled: true
  default: leaf
stt_backend:
  enabled: true
  default: leaf
proxy_backend:
  enabled: true
  default: leaf
backends:
  leaf:
    base_url: %s
    capabilities: [chat, tts, stt, voices]
    models:
      allow:
        - test-model
`, leaf.URL)
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: leaf.URL})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/health/backends", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("health attempt %d: status %d body %q", i, rec.Code, rec.Body.String())
		}
	}
	dialed := n

	body := `{"model":"leaf/test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusServiceUnavailable && strings.Contains(rec.Body.String(), "circuit breaker") {
		t.Fatalf("chat tripped by health probes: status %d body %q", rec.Code, rec.Body.String())
	}
	if n <= dialed {
		t.Fatalf("chat should still dial upstream after health probes: dialed before=%d after=%d", dialed, n)
	}
}
