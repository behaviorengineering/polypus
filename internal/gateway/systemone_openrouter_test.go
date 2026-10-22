package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestSystemOneOpenRouterDecision(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"perplexity/pplx-decider-v1.2","answers":{"is_urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	t.Cleanup(upstream.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
systemone_backend:
  enabled: true
  default: openrouter
chat_backend:
  enabled: true
  default: openrouter
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  openrouter:
    remote: false
    base_url: %s/api/v1
    capabilities: [chat, systemone]
    models:
      systemone_allow:
        - perplexity/pplx-decider-v1.2
`, upstream.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	opts := config.ServeOptions{BackendURL: upstream.URL}
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	client, err := router.NewClient(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)

	handler := newTestGateway(t, opts, client)

	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{
		"model": "openrouter/perplexity/pplx-decider-v1.2",
		"state": "Help! payouts failing for 3 days.",
		"questions": {
			"is_urgent": {
				"type": "noul",
				"instructions": "Does this convey urgency?"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.HasSuffix(gotPath, "/alpha/decisions") {
		t.Fatalf("upstream path %q", gotPath)
	}
	if strings.Contains(gotPath, "/v1/systemone") || strings.Contains(gotPath, "/v1/decisions") {
		t.Fatalf("unexpected upstream path %q", gotPath)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["model"] != "openrouter/perplexity/pplx-decider-v1.2" {
		t.Fatalf("response model %v", resp["model"])
	}
}
