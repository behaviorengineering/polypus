package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestNamedRouterPassthrough(t *testing.T) {
	var gotModel string
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body := readBody(t, r)
		if strings.Contains(body, `"model"`) {
			gotModel = extractJSONField(body, "model")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_chat_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: %s
    capabilities: [chat]
    models:
      allow:
        - "@cf/allow-me"
routers:
  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/allow-me
`, cf.URL)
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/scribe","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	if gotModel != "@cf/allow-me" {
		t.Fatalf("downstream model: %q", gotModel)
	}
}

func TestNamedRouterComposed503WhenSwitchyardDown(t *testing.T) {
	dir := t.TempDir()
	content := `
default_chat_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: http://127.0.0.1:1323
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
        - "@cf/b"
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    models:
      allow:
        - qwen
routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/a
      efficient: lm_studio/qwen
`
	writeConfig(t, dir, content)
	t.Setenv("POLYPUS_SWITCHYARD_BASE_URL", "http://127.0.0.1:1")

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/investigator","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "switchyard") {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestNamedRouterComposedProxiesToSwitchyard(t *testing.T) {
	sw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			body := readBody(t, r)
			if !strings.Contains(body, `"model":"router/investigator"`) {
				t.Fatalf("expected unchanged router model in body: %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"via-switchyard"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(sw.Close)
	t.Setenv("POLYPUS_SWITCHYARD_BASE_URL", sw.URL)

	dir := t.TempDir()
	content := `
default_chat_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: http://127.0.0.1:1323
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
        - "@cf/b"
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    models:
      allow:
        - qwen
routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/a
      efficient: lm_studio/qwen
`
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/investigator","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "via-switchyard") {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestNamedRouterLLMClassifierProxiesToSwitchyard(t *testing.T) {
	sw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			body := readBody(t, r)
			if !strings.Contains(body, `"model":"router/smart"`) {
				t.Fatalf("expected unchanged router model in body: %s", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"via-classifier"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(sw.Close)
	t.Setenv("POLYPUS_SWITCHYARD_BASE_URL", sw.URL)

	dir := t.TempDir()
	content := `
default_chat_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: http://127.0.0.1:1323
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
        - "@cf/b"
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    models:
      allow:
        - qwen
routers:
  smart:
    capability: chat
    route:
      type: llm_classifier
      mode: custom
      classifier: cf_local/@cf/a
      targets:
        fast: lm_studio/qwen
        premium: cf_local/@cf/b
      default_target: premium
      prompt: choose
      response_schema: '{"type":"object","properties":{"x":{"type":"string"}}}'
      policy:
        type: target_selector
        selector: /decision/target
`
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/smart","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "via-classifier") {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestModelsListIncludesRouters(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
default_chat_backend: cf_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: http://127.0.0.1:1323
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/a
      efficient: cf_local/@cf/a
  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/a
`, backend.URL)
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"router/investigator"`) {
		t.Fatalf("missing investigator router: %s", body)
	}
	if !strings.Contains(body, `"id":"router/scribe"`) {
		t.Fatalf("missing scribe router: %s", body)
	}
	if !strings.Contains(body, `"owned_by":"polypus"`) {
		t.Fatalf("missing owned_by polypus: %s", body)
	}
}

func TestNamedRouterVisionRejected(t *testing.T) {
	dir := t.TempDir()
	content := `
default_chat_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: http://127.0.0.1:1323
    capabilities: [chat, vision]
    models:
      allow:
        - "@cf/a"
routers:
  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/a
`
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/scribe","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
}

func TestParseNamedRouterModel(t *testing.T) {
	if name, ok := parseNamedRouterModel("router/investigator"); !ok || name != "investigator" {
		t.Fatalf("got %q ok=%v", name, ok)
	}
	if _, ok := parseNamedRouterModel("cf_local/foo"); ok {
		t.Fatal("expected false for backend model")
	}
}

func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
}

func readBody(t *testing.T, r *http.Request) string {
	t.Helper()
	b := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := r.Body.Read(buf)
		if n > 0 {
			b = append(b, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(b)
}

func extractJSONField(body, key string) string {
	needle := `"` + key + `":`
	i := strings.Index(body, needle)
	if i < 0 {
		return ""
	}
	rest := body[i+len(needle):]
	rest = strings.TrimSpace(rest)
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
