package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/router"
	"github.com/behaviorengineering/polypus/internal/upstream"
)

// fakeRouter satisfies Router without bifrost.Init (UsesBifrost always false).
type fakeRouter struct {
	reg *router.Registry
}

func (f *fakeRouter) Registry() *router.Registry { return f.reg }

func (f *fakeRouter) UsesBifrost(string) bool { return false }

func (f *fakeRouter) ChatCompletionRaw(context.Context, string, string, []byte, time.Duration) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: ChatCompletionRaw not used when UsesBifrost is false")
}

func (f *fakeRouter) ChatCompletionStreamRaw(context.Context, string, string, []byte, time.Duration) (<-chan []byte, <-chan error, error) {
	return nil, nil, fmt.Errorf("fakeRouter: ChatCompletionStreamRaw not used when UsesBifrost is false")
}

func (f *fakeRouter) EmbeddingRaw(context.Context, string, string, []byte, time.Duration) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: EmbeddingRaw not used when UsesBifrost is false")
}

func (f *fakeRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: Synthesize not implemented")
}

func (f *fakeRouter) Transcribe(context.Context, router.TranscriptionRequest) ([]byte, string, error) {
	return nil, "", fmt.Errorf("fakeRouter: Transcribe not implemented")
}

// recordingRouter is a fake that can force Bifrost paths and record dials.
type recordingRouter struct {
	reg             *router.Registry
	uses            map[string]bool
	chatProviders   []string
	streamProviders []string
	embedProviders  []string
}

func (r *recordingRouter) Registry() *router.Registry { return r.reg }

func (r *recordingRouter) UsesBifrost(id string) bool {
	if r.uses != nil {
		if v, ok := r.uses[id]; ok {
			return v
		}
	}
	return false
}

func (r *recordingRouter) ChatCompletionRaw(_ context.Context, backendID, model string, _ []byte, _ time.Duration) ([]byte, error) {
	r.chatProviders = append(r.chatProviders, backendID+":"+model)
	return []byte(`{"choices":[{"message":{"content":"via-bifrost"}}]}`), nil
}

func (r *recordingRouter) ChatCompletionStreamRaw(_ context.Context, backendID, model string, _ []byte, _ time.Duration) (<-chan []byte, <-chan error, error) {
	r.streamProviders = append(r.streamProviders, backendID+":"+model)
	chunks := make(chan []byte, 1)
	errCh := make(chan error)
	go func() {
		defer close(chunks)
		defer close(errCh)
		chunks <- []byte(`{"id":"1","object":"chat.completion.chunk","choices":[{"delta":{"content":"hi"}}]}`)
	}()
	return chunks, errCh, nil
}

func (r *recordingRouter) EmbeddingRaw(_ context.Context, backendID, model string, _ []byte, _ time.Duration) ([]byte, error) {
	r.embedProviders = append(r.embedProviders, backendID+":"+model)
	return []byte(`{"data":[{"embedding":[0.1]}]}`), nil
}

func (r *recordingRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, fmt.Errorf("recordingRouter: Synthesize not implemented")
}

func (r *recordingRouter) Transcribe(context.Context, router.TranscriptionRequest) ([]byte, string, error) {
	return nil, "", fmt.Errorf("recordingRouter: Transcribe not implemented")
}

func newFakeRouter(t *testing.T, opts config.ServeOptions) *fakeRouter {
	t.Helper()
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := router.NewRegistry(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeRouter{reg: reg}
}

// newTestGateway builds a handler with an injected Router (skips bifrost.Init).
func newTestGateway(t *testing.T, opts config.ServeOptions, r Router, extra ...HandlerOption) http.Handler {
	t.Helper()
	options := append([]HandlerOption{WithRouter(r)}, extra...)
	handler, err := NewHandler(opts, options...)
	if err != nil {
		t.Fatal(err)
	}
	if gw, ok := handler.(*Gateway); ok {
		t.Cleanup(gw.Close)
	}
	return handler
}

func TestNewHandlerWithRouterSkipsBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"via-fake"}}]}`))
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

	opts := config.ServeOptions{BackendURL: leaf.URL}
	fake := newFakeRouter(t, opts)
	handler := newTestGateway(t, opts, fake)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"leaf/test-model","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "via-fake") {
		t.Fatalf("body: %q", rec.Body.String())
	}
	gw := handler.(*Gateway)
	if gw.ownedRouter {
		t.Fatal("injected router must not be owned")
	}
}

func TestWithRouterNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = WithRouter(nil)
}

func TestSwitchyardNameAligned(t *testing.T) {
	if router.ProviderSwitchyard != upstream.NameSwitchyard {
		t.Fatalf("ProviderSwitchyard=%q NameSwitchyard=%q", router.ProviderSwitchyard, upstream.NameSwitchyard)
	}
}

func TestLeafChatViaRecordingBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	leafHits := 0
	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leafHits++
		http.Error(w, "leaf must not be dialed", http.StatusBadGateway)
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

	opts := config.ServeOptions{BackendURL: leaf.URL}
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := router.NewRegistry(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	recRouter := &recordingRouter{reg: reg, uses: map[string]bool{"leaf": true}}
	handler := newTestGateway(t, opts, recRouter)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"leaf/test-model","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "via-bifrost") {
		t.Fatalf("body: %q", rec.Body.String())
	}
	if leafHits != 0 {
		t.Fatalf("leaf HTTP dials=%d", leafHits)
	}
	if len(recRouter.chatProviders) != 1 || recRouter.chatProviders[0] != "leaf:test-model" {
		t.Fatalf("chatProviders=%v", recRouter.chatProviders)
	}
}

func TestLeafChatStreamViaRecordingBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	dir := t.TempDir()
	content := `
default_chat_backend: leaf
default_tts_backend: leaf
default_stt_backend: leaf
default_proxy_backend: leaf
backends:
  leaf:
    base_url: http://127.0.0.1:9
    capabilities: [chat, tts, stt, voices]
    models:
      allow:
        - test-model
`
	writeConfig(t, dir, content)

	opts := config.ServeOptions{BackendURL: "http://127.0.0.1:9"}
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := router.NewRegistry(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	recRouter := &recordingRouter{reg: reg, uses: map[string]bool{"leaf": true}}
	handler := newTestGateway(t, opts, recRouter)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"leaf/test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: ") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("sse body: %q", body)
	}
	if len(recRouter.streamProviders) != 1 || recRouter.streamProviders[0] != "leaf:test-model" {
		t.Fatalf("streamProviders=%v", recRouter.streamProviders)
	}
}

func TestSwitchyardComposedViaRecordingBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "1")
	t.Setenv("POLYPUS_SWITCHYARD_BASE_URL", "http://127.0.0.1:1")

	dir := t.TempDir()
	content := `
default_chat_backend: leaf
default_tts_backend: leaf
default_stt_backend: leaf
default_proxy_backend: leaf
backends:
  leaf:
    base_url: http://127.0.0.1:9
    capabilities: [chat, tts, stt, voices]
    models:
      allow:
        - a
        - b
routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: leaf/a
      efficient: leaf/b
`
	writeConfig(t, dir, content)

	opts := config.ServeOptions{BackendURL: "http://127.0.0.1:9"}
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := router.NewRegistry(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	recRouter := &recordingRouter{
		reg:  reg,
		uses: map[string]bool{router.ProviderSwitchyard: true},
	}
	handler := newTestGateway(t, opts, recRouter)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/investigator","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "via-bifrost") {
		t.Fatalf("body: %q", rec.Body.String())
	}
	want := router.ProviderSwitchyard + ":router/investigator"
	if len(recRouter.chatProviders) != 1 || recRouter.chatProviders[0] != want {
		t.Fatalf("chatProviders=%v want %q", recRouter.chatProviders, want)
	}
}

func TestCFLeafChatViaRecordingBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "test-key")

	cfHits := 0
	cf := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		cfHits++
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
default_chat_backend: cf_local
default_tts_backend: cf_local
default_stt_backend: cf_local
default_proxy_backend: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: %s/client/v4/accounts/acct-test/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, embed, tts, stt, voices]
    models:
      allow:
        - "@cf/allow-me"
`, cf.URL)
	writeConfig(t, dir, content)

	opts := config.ServeOptions{BackendURL: cf.URL}
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := router.NewRegistry(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	recRouter := &recordingRouter{reg: reg, uses: map[string]bool{"cf_local": true}}
	handler := newTestGateway(t, opts, recRouter)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"cf_local/@cf/allow-me","messages":[{"role":"user","content":"hi"}]}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if cfHits != 0 {
		t.Fatalf("direct CF dials=%d", cfHits)
	}
	if len(recRouter.chatProviders) != 1 || recRouter.chatProviders[0] != "cf_local:@cf/allow-me" {
		t.Fatalf("chatProviders=%v", recRouter.chatProviders)
	}
}

func TestCFEmbedViaRecordingBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "test-key")

	dir := t.TempDir()
	content := `
default_chat_backend: cf_local
default_embed_backend: cf_local
default_tts_backend: cf_local
default_stt_backend: cf_local
default_proxy_backend: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: http://127.0.0.1:9/client/v4/accounts/acct-test/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, embed, tts, stt, voices]
    models:
      allow:
        - "@cf/embed-me"
`
	writeConfig(t, dir, content)

	opts := config.ServeOptions{BackendURL: "http://127.0.0.1:9"}
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := router.NewRegistry(rcfg)
	if err != nil {
		t.Fatal(err)
	}
	recRouter := &recordingRouter{reg: reg, uses: map[string]bool{"cf_local": true}}
	handler := newTestGateway(t, opts, recRouter)

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(
		`{"model":"cf_local/@cf/embed-me","input":"hi"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if len(recRouter.embedProviders) != 1 || recRouter.embedProviders[0] != "cf_local:@cf/embed-me" {
		t.Fatalf("embedProviders=%v", recRouter.embedProviders)
	}
}
