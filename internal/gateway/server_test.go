package gateway

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestModelsAllowFilterAndInventory(t *testing.T) {
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"tts-a","object":"model"},{"id":"tts-hidden","object":"model"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mlx.Close)

	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"@cf/allow-me","object":"model"},{"id":"@cf/deny-me","object":"model"}]}`))
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
default_chat_backend: cf_local
default_vision_backend: cf_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
    models:
      allow:
        - "tts-a"
  cf_local:
    base_url: %s
    capabilities: [chat, vision, tts, stt, voices]
    models:
      allow:
        - "@cf/allow-me"
`, mlx.URL, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("POLYPUS_MODELS_CACHE", filepath.Join(dir, "cache.json"))
	t.Setenv("POLYPUS_DEFAULT_MODEL", "")
	t.Setenv("POLYPUS_DEFAULT_STT_MODEL", "")

	handler, err := NewHandler(config.ServeOptions{BackendURL: mlx.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cf_local/@cf/allow-me") {
		t.Fatalf("missing allow: %s", body)
	}
	if strings.Contains(body, "deny-me") {
		t.Fatalf("deny leaked into enabled list: %s", body)
	}
	if strings.Contains(body, "tts-hidden") {
		t.Fatalf("hidden tts leaked: %s", body)
	}
	if !strings.Contains(body, "mlx_local/tts-a") {
		t.Fatalf("missing tts-a: %s", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models?view=inventory", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	inv := rec.Body.String()
	if !strings.Contains(inv, "deny-me") || !strings.Contains(inv, "tts-hidden") {
		t.Fatalf("inventory should include non-allowlisted: %s", inv)
	}

	// Disallowed chat
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cf_local/@cf/deny-me","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("deny chat status: %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_not_allowed") {
		t.Fatalf("deny chat body: %s", rec.Body.String())
	}

	// Allowed chat
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cf_local/@cf/allow-me","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("allow chat: %d %s", rec.Code, rec.Body.String())
	}
}

func TestModelsListAggregatesBackends(t *testing.T) {
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"tts-local","object":"model","created":1,"owned_by":"mlx"}]}`))
	}))
	t.Cleanup(mlx.Close)

	lm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"nomic-embed","object":"model","created":2,"owned_by":"lm"}]}`))
	}))
	t.Cleanup(lm.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
default_embed_backend: lm_studio
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
  lm_studio:
    base_url: %s/v1
    capabilities: [chat, vision, embed]
`, mlx.URL, lm.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("POLYPUS_DEFAULT_MODEL", "")
	t.Setenv("POLYPUS_DEFAULT_STT_MODEL", "")

	handler, err := NewHandler(config.ServeOptions{BackendURL: mlx.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"object":"list"`) {
		t.Fatalf("body: %q", body)
	}
	if !strings.Contains(body, `"id":"mlx_local/tts-local"`) {
		t.Fatalf("missing mlx model: %q", body)
	}
	if !strings.Contains(body, `"id":"lm_studio/nomic-embed"`) {
		t.Fatalf("missing lm model: %q", body)
	}
}

func TestModelsRetrieveAndNotFound(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"whisper-x","object":"model","created":0,"owned_by":"local"}]}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
`, backend.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models/mlx_local/whisper-x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retrieve status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"mlx_local/whisper-x"`) {
		t.Fatalf("retrieve body: %q", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models/does-not-exist", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_not_found") {
		t.Fatalf("missing body: %q", rec.Body.String())
	}
}

func TestModelsListFallsBackToEnvDefaults(t *testing.T) {
	// Backend is up but has no /v1/models.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
`, backend.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("POLYPUS_DEFAULT_MODEL", "mlx-community/tts-env")
	t.Setenv("POLYPUS_DEFAULT_STT_MODEL", "mlx-community/stt-env")

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "mlx_local/mlx-community/tts-env") {
		t.Fatalf("missing tts seed: %q", body)
	}
	if !strings.Contains(body, "mlx_local/mlx-community/stt-env") {
		t.Fatalf("missing stt seed: %q", body)
	}
	// Unprefixed ids for default backend so bare model names stay tool-usable.
	if !strings.Contains(body, `"id":"mlx-community/tts-env"`) {
		t.Fatalf("missing bare tts id: %q", body)
	}
}

func TestOpenAIModelsURL(t *testing.T) {
	if got := openAIModelsURL("http://127.0.0.1:1234/v1"); got != "http://127.0.0.1:1234/v1/models" {
		t.Fatalf("with /v1: %q", got)
	}
	if got := openAIModelsURL("http://127.0.0.1:1322"); got != "http://127.0.0.1:1322/v1/models" {
		t.Fatalf("without /v1: %q", got)
	}
}

func TestHealthOKWhenBackendUp(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	opts := config.ServeOptions{
		Host:       "127.0.0.1",
		Port:       1320,
		BackendURL: backend.URL,
	}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"router":"bifrost"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"backends"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestBifrostTranscriptionPath(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))
	t.Cleanup(backend.Close)

	opts := config.ServeOptions{BackendURL: backend.URL}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("model", "whisper-test")
	_ = w.WriteField("response_format", "json")
	part, _ := w.CreateFormFile("file", "test.mp3")
	_, _ = part.Write([]byte("fake-audio"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Fatalf("backend path: got %q", gotPath)
	}
	if !strings.Contains(rec.Body.String(), `"text":"ok"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestSTTRoutesToPrefixBackend(t *testing.T) {
	var gotHost string
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mlx should not receive prefixed alt_stt request", http.StatusTeapot)
	}))
	t.Cleanup(mlx.Close)

	alt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"alt-ok"}`))
	}))
	t.Cleanup(alt.Close)

	dir := t.TempDir()
	altURL := alt.URL
	mlxURL := mlx.URL
	content := fmt.Sprintf(`
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
  alt_stt:
    base_url: %s
    capabilities: [stt]
`, mlxURL, altURL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	opts := config.ServeOptions{BackendURL: mlxURL}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.WriteField("model", "alt_stt/whisper-test")
	_ = w.WriteField("response_format", "json")
	part, _ := w.CreateFormFile("file", "test.mp3")
	_, _ = part.Write([]byte("fake"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alt-ok") {
		t.Fatalf("body: %q", rec.Body.String())
	}
	altHost := strings.TrimPrefix(strings.TrimPrefix(altURL, "http://"), "https://")
	if gotHost != altHost {
		t.Fatalf("host: got %q want %q", gotHost, altHost)
	}
}

func TestBifrostSpeechPath(t *testing.T) {
	var gotPath string
	var gotBody []byte
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "audio/mp3")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-audio"))
	}))
	t.Cleanup(backend.Close)

	opts := config.ServeOptions{BackendURL: backend.URL}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(
		`{"model":"tts-test","input":"hello","voice":"vivian","response_format":"mp3"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/audio/speech" {
		t.Fatalf("backend path: got %q", gotPath)
	}
	if !strings.Contains(string(gotBody), "hello") {
		t.Fatalf("backend body: %q", gotBody)
	}
}

func TestChatNotProxiedToMLXWhenUnconfigured(t *testing.T) {
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mlx must not receive chat", http.StatusTeapot)
	}))
	t.Cleanup(mlx.Close)

	opts := config.ServeOptions{BackendURL: mlx.URL}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"glm","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
}

func TestChatRoutesToCFBackend(t *testing.T) {
	var gotHost string
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mlx must not receive chat", http.StatusTeapot)
	}))
	t.Cleanup(mlx.Close)

	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path: %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}]}`))
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_chat_backend: cf_local
default_vision_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: %s
    capabilities: [chat, vision]
`, mlx.URL, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	handler, err := NewHandler(config.ServeOptions{BackendURL: mlx.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cf_local/@cf/zai-org/glm-4.7-flash","messages":[{"role":"user","content":"ping"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	cfHost := strings.TrimPrefix(strings.TrimPrefix(cf.URL, "http://"), "https://")
	if gotHost != cfHost {
		t.Fatalf("host: got %q want %q", gotHost, cfHost)
	}
}

func TestVisionRoutesToCFBackend(t *testing.T) {
	var gotBody []byte
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_chat_backend: cf_local
default_vision_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: %s
    capabilities: [chat, vision]
`, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"@cf/google/gemma-4-26b-a4b-it","messages":[{"role":"user","content":[{"type":"text","text":"judge"},{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(gotBody), "gemma-4-26b") {
		t.Fatalf("body: %q", gotBody)
	}
}

func TestEmbedNotProxiedToMLXWhenUnconfigured(t *testing.T) {
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "mlx must not receive embed", http.StatusTeapot)
	}))
	t.Cleanup(mlx.Close)

	opts := config.ServeOptions{BackendURL: mlx.URL}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(
		`{"model":"text-embedding-nomic-embed-text-v1.5","input":["probe"]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
}

func TestEmbedRoutesToLMStudioBackend(t *testing.T) {
	var gotBody []byte
	lm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("path: %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`))
	}))
	t.Cleanup(lm.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_embed_backend: lm_studio
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  lm_studio:
    base_url: %s/v1
    capabilities: [chat, vision, embed]
`, lm.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(
		`{"model":"lm_studio/text-embedding-nomic-embed-text-v1.5","input":["probe"]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(gotBody), "text-embedding-nomic-embed-text-v1.5") {
		t.Fatalf("body: %q", gotBody)
	}
	if strings.Contains(string(gotBody), "lm_studio/") {
		t.Fatalf("backend prefix should be stripped: %q", gotBody)
	}
}

func TestCloudflareExtensionGatewayInventory(t *testing.T) {
	mlx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(mlx.Close)

	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ai/models/search"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"success": true,
				"result": [{"name": "@cf/zai-org/glm-4.7-flash"}],
				"result_info": {"page": 1, "total_pages": 1}
			}`))
		case r.URL.Path == "/client/v4/accounts/acct-test/ai/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_chat_backend: cf_local
default_vision_backend: cf_local
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
backends:
  mlx_local:
    base_url: %s
    capabilities: [tts, stt, voices]
  cf_local:
    remote: true
    extension: cloudflare
    base_url: %s/client/v4/accounts/acct-test/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, vision]
    models:
      allow:
        - "@cf/zai-org/glm-4.7-flash"
`, mlx.URL, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")

	handler, err := NewHandler(config.ServeOptions{BackendURL: mlx.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models?view=inventory", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inventory status: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cf_local/@cf/zai-org/glm-4.7-flash") {
		t.Fatalf("inventory body: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cf_local/@cf/zai-org/glm-4.7-flash","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("chat status: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"cf_local"`) || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("health body: %s", rec.Body.String())
	}
}

func TestSpeechEmptyModelUsesDefaultBackend(t *testing.T) {
	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "audio/mp3")
		_, _ = w.Write([]byte("fake-audio"))
	}))
	t.Cleanup(backend.Close)

	t.Setenv("POLYPUS_DEFAULT_MODEL", "tts-test")
	opts := config.ServeOptions{BackendURL: backend.URL}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(
		`{"input":"hello","voice":"vivian","response_format":"mp3"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/audio/speech" {
		t.Fatalf("backend path: got %q", gotPath)
	}
}
