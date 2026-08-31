package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestLeafChatCircuitOpensAfterFailures(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	n := 0
	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(leaf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_chat_backend: leaf
default_tts_backend: leaf
default_stt_backend: leaf
default_proxy_backend: leaf
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

	// stream:true forces the HTTP proxy path (not Bifrost).
	body := `{"model":"leaf/test-model","messages":[{"role":"user","content":"hi"}],"stream":true}`
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("attempt %d: status %d body %q", i, rec.Code, rec.Body.String())
		}
	}
	dialed := n
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("open status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Fatalf("body: %q", rec.Body.String())
	}
	if n != dialed {
		t.Fatalf("circuit open still dialed: before=%d after=%d", dialed, n)
	}
}
