package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// fakeRouter satisfies Router without bifrost.Init. UsesBifrost is always false, so
// chat and embeddings through it fail closed; tests that dial use scriptedRouter or
// recordingRouter.
type fakeRouter struct {
	reg *router.Registry
}

func (f *fakeRouter) Registry() *router.Registry { return f.reg }

func (f *fakeRouter) UsesBifrost(string) bool { return false }

func (f *fakeRouter) ChatCompletionRaw(context.Context, router.Call) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: ChatCompletionRaw not used when UsesBifrost is false")
}

func (f *fakeRouter) ChatCompletionStreamRaw(context.Context, router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	return nil, nil, fmt.Errorf("fakeRouter: ChatCompletionStreamRaw not used when UsesBifrost is false")
}

func (f *fakeRouter) EmbeddingRaw(context.Context, router.Call) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: EmbeddingRaw not used when UsesBifrost is false")
}

func (f *fakeRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: Synthesize not implemented")
}

func (f *fakeRouter) Transcribe(context.Context, router.TranscriptionRequest) ([]byte, string, error) {
	return nil, "", fmt.Errorf("fakeRouter: Transcribe not implemented")
}

func (f *fakeRouter) SystemOneDecisionRaw(context.Context, router.Call) ([]byte, error) {
	return nil, fmt.Errorf("fakeRouter: SystemOneDecisionRaw not used when UsesBifrost is false")
}

type errSynthRouter struct {
	*fakeRouter
	err error
}

func (e *errSynthRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, e.err
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

func (r *recordingRouter) ChatCompletionRaw(_ context.Context, call router.Call) ([]byte, error) {
	r.chatProviders = append(r.chatProviders, call.BackendID+":"+call.Model)
	return []byte(`{"choices":[{"message":{"content":"via-bifrost"}}]}`), nil
}

func (r *recordingRouter) ChatCompletionStreamRaw(_ context.Context, call router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	r.streamProviders = append(r.streamProviders, call.BackendID+":"+call.Model)
	chunks := make(chan router.ChatStreamChunk, 1)
	errCh := make(chan error)
	go func() {
		defer close(chunks)
		defer close(errCh)
		chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"1","object":"chat.completion.chunk","choices":[{"delta":{"content":"hi"}}]}`)}
	}()
	return chunks, errCh, nil
}

func (r *recordingRouter) EmbeddingRaw(_ context.Context, call router.Call) ([]byte, error) {
	r.embedProviders = append(r.embedProviders, call.BackendID+":"+call.Model)
	return []byte(`{"data":[{"embedding":[0.1]}]}`), nil
}

func (r *recordingRouter) Synthesize(context.Context, router.SpeechRequest) ([]byte, error) {
	return nil, fmt.Errorf("recordingRouter: Synthesize not implemented")
}

func (r *recordingRouter) Transcribe(context.Context, router.TranscriptionRequest) ([]byte, string, error) {
	return nil, "", fmt.Errorf("recordingRouter: Transcribe not implemented")
}

func (r *recordingRouter) SystemOneDecisionRaw(context.Context, router.Call) ([]byte, error) {
	return nil, fmt.Errorf("recordingRouter: SystemOneDecisionRaw not implemented")
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
func newTestGateway(t *testing.T, opts config.ServeOptions, r registryProvider, extra ...HandlerOption) http.Handler {
	t.Helper()
	options := append([]HandlerOption{withRouter(r)}, extra...)
	handler, err := NewHandler(opts, options...)
	if err != nil {
		t.Fatal(err)
	}
	switch h := handler.(type) {
	case *accessProtector:
		t.Cleanup(h.Close)
	case *gatewayHandler:
		t.Cleanup(h.Close)
	}
	return handler
}

func TestNewHandlerWithRouterSkipsBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	leafHits := 0
	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leafHits++
		http.Error(w, "leaf must not be dialed", http.StatusBadGateway)
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

	opts := config.ServeOptions{BackendURL: leaf.URL}
	fake := newFakeRouter(t, opts)
	recRouter := &recordingRouter{reg: fake.reg, uses: map[string]bool{"leaf": true}}
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
	if !strings.Contains(rec.Body.String(), "via-bifrost") || leafHits != 0 {
		t.Fatalf("the injected router must answer, not the leaf: body %q leaf hits %d", rec.Body.String(), leafHits)
	}
	gw, ok := gatewayFromHandler(handler)
	if !ok {
		t.Fatal("expected gateway handler")
	}
	if gw.closeRouter != nil {
		t.Fatal("injected router must not be owned")
	}
}

func TestWithRouterNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = withRouter(nil)
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

func TestSwitchyardComposedViaBifrostKeepsTheTrace(t *testing.T) {
	exporter := installTestTracer(t)
	prevProp := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))
	t.Cleanup(func() { otel.SetTextMapPropagator(prevProp) })
	tp := otel.GetTracerProvider()

	t.Setenv("POLYPUS_SWITCHYARD", "1")
	var gotTraceparent, gotPath string
	var gotBody []byte
	sy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTraceparent = r.Header.Get("traceparent")
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Switchyard-Selected-Model", "leaf/b")
		w.Header().Set("X-Switchyard-Internal", "not for the caller")
		_, _ = w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"leaf/b","choices":[{"index":0,"message":{"role":"assistant","content":"via-switchyard"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(sy.Close)
	t.Setenv("POLYPUS_SWITCHYARD_BASE_URL", sy.URL)

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

	// No injected router: the gateway builds a real Bifrost client, which registers
	// Switchyard as a provider.
	inner, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:9"}, WithAdminStateDir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if gw, ok := gatewayFromHandler(inner); ok {
		t.Cleanup(gw.Close)
	}
	handler := observability.WrapHandler(inner, nil)

	ctx, root := tp.Tracer("client").Start(context.Background(), "test.client")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"router/investigator","messages":[{"role":"user","content":"hi"}]}`,
	))
	req = req.WithContext(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	root.End()
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "via-switchyard") {
		t.Fatalf("body: %q", rec.Body.String())
	}
	if gotPath != "/v1/chat/completions" || !strings.Contains(string(gotBody), `"router/investigator"`) {
		t.Fatalf("Switchyard must get the client's body on the chat path: %s %s", gotPath, gotBody)
	}
	if gotTraceparent == "" || !strings.Contains(gotTraceparent, root.SpanContext().TraceID().String()) {
		t.Fatalf("Switchyard must get the request's trace: traceparent %q", gotTraceparent)
	}
	if rec.Header().Get("X-Switchyard-Selected-Model") != "leaf/b" || rec.Header().Get("X-Switchyard-Internal") != "" {
		t.Fatalf("only the selected-model header reaches the caller: %v", rec.Header())
	}
	if got := spanAttr(exporter, "polypus.router", "polypus.downstream_model"); got != "leaf/b" {
		t.Fatalf("downstream model on the router span = %q", got)
	}
}

func TestCFLeafChatViaRecordingBifrost(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("CF_AI_API_KEY", "test-key")

	cfHits := 0
	cf := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		cfHits++
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
chat_backend:
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
	t.Setenv("CF_AI_API_KEY", "test-key")

	dir := t.TempDir()
	content := `
chat_backend:
  enabled: true
  default: cf_local
embed_backend:
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

func TestSpeechDomainErrorMapsHTTPStatus(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	leaf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "leaf must not be dialed", http.StatusBadGateway)
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
`, leaf.URL)
	writeConfig(t, dir, content)
	opts := config.ServeOptions{BackendURL: leaf.URL}
	base := newFakeRouter(t, opts)

	cases := []struct {
		name string
		err  error
		code int
	}{
		{"invalid", derrors.New(derrors.CodeInvalid, "router.Synthesize", "input required"), http.StatusBadRequest},
		{"timeout", derrors.New(derrors.CodeTimeout, "cloudflare.Synthesize", "deadline"), http.StatusGatewayTimeout},
		{"unavailable", derrors.New(derrors.CodeUnavailable, "router.bifrost", "provider call"), http.StatusBadGateway},
		{"rate_limited", derrors.New(derrors.CodeRateLimited, "router.bifrost", "provider rate limited").
			With("cf_code", "3036"), http.StatusTooManyRequests},
		{"plain", fmt.Errorf("not a domain error"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestGateway(t, opts, &errSynthRouter{fakeRouter: base, err: tc.err})
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("status=%d want %d body=%q", rec.Code, tc.code, rec.Body.String())
			}
		})
	}
}
