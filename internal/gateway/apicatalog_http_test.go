package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestAPICatalogHTTPAndSurfaceSplit(t *testing.T) {
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"@cf/a","object":"model"},{"id":"typesafe/jev","object":"model"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
chat_backend:
  enabled: true
  default: cf_local
systemone_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
proxy_backend:
  enabled: true
  default: cf_local
backends:
  cf_local:
    base_url: %s
    capabilities: [chat, systemone, tts, stt, voices]
    models:
      allow:
        - "@cf/a"
        - "typesafe/jev"
`, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("POLYPUS_DEFAULT_MODEL", "")
	t.Setenv("POLYPUS_DEFAULT_STT_MODEL", "")

	handler, err := NewHandler(config.ServeOptions{BackendURL: cf.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/apis", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hub status %d %s", rec.Code, rec.Body.String())
	}
	var hub apiCatalog
	if err := json.Unmarshal(rec.Body.Bytes(), &hub); err != nil {
		t.Fatal(err)
	}
	if hub.Object != "api_catalog" || len(hub.APIs) != 2 {
		t.Fatalf("hub: %#v", hub)
	}
	if hub.APIs[0].Schema != "/v1/apis/openai/openapi.yaml" {
		t.Fatalf("openai schema link: %q", hub.APIs[0].Schema)
	}

	openaiBody := getModelsList(t, handler, "/v1/apis/openai/models")
	aliasBody := getModelsList(t, handler, "/v1/models")
	if openaiBody != aliasBody {
		t.Fatalf("alias mismatch:\nopenai %s\nalias %s", openaiBody, aliasBody)
	}
	if strings.Contains(openaiBody, "typesafe/jev") {
		t.Fatalf("openai list leaked JEV: %s", openaiBody)
	}
	if !strings.Contains(openaiBody, "cf_local/@cf/a") {
		t.Fatalf("missing chat model: %s", openaiBody)
	}
	link := rec.Header().Get("Link")
	_ = link

	sysBody := getModelsList(t, handler, "/v1/apis/systemone/models")
	if strings.Contains(sysBody, `"id":"typesafe/jev"`) {
		t.Fatalf("systemone should not list bare alias duplicate: %s", sysBody)
	}
	if !strings.Contains(sysBody, "cf_local/typesafe/jev") {
		t.Fatalf("systemone missing JEV: %s", sysBody)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models/typesafe/jev", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("legacy retrieve JEV: status %d body %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/apis/systemone/models/typesafe/jev", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("systemone retrieve: %d %s", rec.Code, rec.Body.String())
	}
}

func getModelsList(t *testing.T, handler http.Handler, path string) string {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s status %d %s", path, rec.Code, rec.Body.String())
	}
	if path == "/v1/models" || path == "/v1/apis/openai/models" {
		if link := rec.Header().Get("Link"); !strings.Contains(link, openAIModelsCanonicalPath) {
			t.Fatalf("%s missing canonical Link: %q", path, link)
		}
	}
	return rec.Body.String()
}

func TestAPIDocsServe(t *testing.T) {
	dir := t.TempDir()
	content := `
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
    base_url: http://127.0.0.1:9
    capabilities: [chat, tts, stt, voices]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/apis/openai/openapi.yaml", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("openapi status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/v1/chat/completions") {
		t.Fatalf("openapi body: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/apis/systemone/schema.json", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("systemone schema without backend: %d", rec.Code)
	}
}
