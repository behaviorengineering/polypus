package router

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
	"github.com/maximhq/bifrost/core/schemas"
)

// testLogger is the logger NewClient requires, discarding everything.
func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestNewClientRequiresLogger(t *testing.T) {
	_, err := NewClient(t.Context(), config.RouterConfig{Timeouts: config.DefaultTimeouts()})
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a missing logger must be CodeInvalid, got %v", err)
	}
}

func badBackendConfig() config.RouterConfig {
	return config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"bad_leaf": {ID: "bad_leaf", BaseURL: "ftp://127.0.0.1:1", Capabilities: []config.Capability{config.CapChat}},
		},
	}
}

func TestNewRegistryTypesABadBackend(t *testing.T) {
	_, err := NewRegistry(badBackendConfig())
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a bad base_url is the operator's input, got %v", err)
	}
	if derrors.Field(err, fieldBackend) != "bad_leaf" {
		t.Fatalf("the error must name the backend: %v", err)
	}
}

func TestNewClientKeepsTheRegistryCode(t *testing.T) {
	_, err := NewClient(t.Context(), badBackendConfig(), WithLogger(testLogger()))
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a bad backend must not read as a Polypus bug (CodeInternal), got %v", err)
	}
}

func TestSpeechTargetKeepsResolveCodes(t *testing.T) {
	reg := testRegistry(t)
	c := &Client{reg: reg}
	provider, model, err := c.speechTarget(speechKind{op: "router.Synthesize", what: "tts", resolve: reg.ResolveTTS, defaultModel: "default-voice"}, "")
	if err != nil || provider != "mlx_local" || model != "default-voice" {
		t.Fatalf("default route: %q %q %v", provider, model, err)
	}
	if _, _, err := c.speechTarget(speechKind{op: "router.Synthesize", what: "tts", resolve: reg.ResolveTTS, defaultModel: ""}, "mlx_local/"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a bad request must stay CodeInvalid (400), got %v", err)
	}
	if _, _, err := c.speechTarget(speechKind{op: "router.Synthesize", what: "tts", resolve: reg.ResolveTTS, defaultModel: ""}, "alt_stt/whisper"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a backend without the capability must stay CodeInvalid (400), got %v", err)
	}

	cfg := reg.Config()
	cfg.TTS.Default = "gone"
	noDefault := &Registry{}
	noDefault.storeCfg(cfg)
	c = &Client{reg: noDefault}
	if _, _, err := c.speechTarget(speechKind{op: "router.Synthesize", what: "tts", resolve: noDefault.ResolveTTS, defaultModel: ""}, ""); derrors.CodeOf(err) != derrors.CodeNotReady {
		t.Fatalf("a missing default backend must stay CodeNotReady (503), got %v", err)
	}
}

func TestAnthropicChatErrKeepsProviderCause(t *testing.T) {
	cause := derrors.New(derrors.CodeInvalid, "provider", "bad tool schema").With("param", "tools.0")
	berr := bifrostTestErr(400, "invalid_request_error", "tools.0: invalid schema")
	berr.Error.Error = cause
	err := anthropicChatErr(berr)
	if derrors.HTTPStatus(err) != http.StatusBadRequest {
		t.Fatalf("status: %v", err)
	}
	if !errors.Is(err, cause) || derrors.Field(err, "param") != "tools.0" {
		t.Fatalf("the provider cause must stay in the chain: %+v", err)
	}
	if derrors.Field(err, ratelimit.FieldStatus) != "400" {
		t.Fatalf("status field: %+v", err)
	}
}

func TestBifrostErrRateLimitKeepsProviderCause(t *testing.T) {
	cause := errors.New("provider says slow down")
	status := http.StatusTooManyRequests
	err := bifrostErr(&schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "slow down", Error: cause},
	}, nil, false)
	if derrors.CodeOf(err) != derrors.CodeRateLimited {
		t.Fatalf("code: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("the provider cause must stay in the chain: %v", err)
	}
	if derrors.Field(err, ratelimit.FieldStatus) != "429" {
		t.Fatalf("the rate limit's fields must win: %+v", err)
	}
}

// TestMapBifrostErrUsesTheKnownBackend shows why the speech paths pass the resolved
// backend id: the Cloudflare speech plugin's error names no provider, so without the
// id a Cloudflare throttle code is not read.
func TestMapBifrostErrUsesTheKnownBackend(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret")
	reg, err := NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Remote:       true,
				Extension:    config.ExtensionCloudflare,
				BaseURL:      "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
				Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{config.CapTTS},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{reg: reg}
	pluginErr := func() *schemas.BifrostError {
		inner := derrors.New(derrors.CodeUnavailable, "cloudflare.speech", "capacity exceeded")
		return &schemas.BifrostError{
			Error: &schemas.ErrorField{Message: inner.Error(), Error: inner},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: json.RawMessage(`{"success":false,"errors":[{"code":3040,"message":"capacity exceeded"}]}`),
			},
		}
	}
	if got := c.mapBifrostErr("", pluginErr(), nil); derrors.CodeOf(got) == derrors.CodeRateLimited {
		t.Fatalf("without a backend id the throttle is not Cloudflare's: %v", got)
	}
	got := c.mapBifrostErr("cf_local", pluginErr(), nil)
	if derrors.CodeOf(got) != derrors.CodeRateLimited || derrors.Field(got, ratelimit.FieldKind) != ratelimit.KindCapacity {
		t.Fatalf("the backend id must select Cloudflare classification: %+v", got)
	}
}

func TestBifrostContextCancelReleasesIt(t *testing.T) {
	parent, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	c := &Client{}
	bctx, cancel := c.bifrostContext(parent, time.Minute, true)
	if bctx.Value(schemas.BifrostContextKeyUseRawRequestBody) != true {
		t.Fatal("raw body flag not set")
	}
	cancel()
	select {
	case <-bctx.Done():
	case <-time.After(time.Second):
		t.Fatal("cancel must end the Bifrost context (and its watcher goroutine)")
	}
	if !errors.Is(bctx.Err(), context.Canceled) {
		t.Fatalf("err=%v", bctx.Err())
	}
	if parent.Err() != nil {
		t.Fatal("cancel must not end the caller's context")
	}
	if bctx.Value(schemas.BifrostContextKeyUseRawRequestBody) != true {
		t.Fatal("values must stay readable after cancel")
	}
}

func TestAccountErrorsAreTyped(t *testing.T) {
	t.Setenv("MISSING_TEST_KEY", "")
	acct := newAccount(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"remote_leaf": {
				ID:           "remote_leaf",
				Remote:       true,
				BaseURL:      "https://llm.example.com/v1",
				Auth:         config.BackendAuth{BearerEnv: "MISSING_TEST_KEY"},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	})
	_, err := acct.GetKeysForProvider(context.Background(), "remote_leaf")
	if err == nil {
		t.Fatal("expected a credential error")
	}
	if code := derrors.CodeOf(err); code == derrors.CodeUnavailable || code == derrors.CodeInternal {
		t.Fatalf("a missing credential must not read as a provider failure, got %v", err)
	}
	if _, err := acct.GetKeysForProvider(context.Background(), "nope"); derrors.CodeOf(err) != derrors.CodeNotFound || derrors.Field(err, fieldProvider) != "nope" {
		t.Fatalf("unknown provider: %v", err)
	}
	if _, err := acct.GetConfigForProvider("nope"); derrors.CodeOf(err) != derrors.CodeNotFound {
		t.Fatalf("unknown provider: %v", err)
	}
}

func TestNewClientRequiresContext(t *testing.T) {
	var nilCtx context.Context // Nil on purpose.
	if _, err := NewClient(nilCtx, config.RouterConfig{Timeouts: config.DefaultTimeouts()}, WithLogger(testLogger())); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a nil context must be CodeInvalid, got %v", err)
	}
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewClient(done, leafConfig("http://127.0.0.1:9"), WithLogger(testLogger())); derrors.CodeOf(err) != derrors.CodeCanceled {
		t.Fatalf("a cancelled start must not init Bifrost, got %v", err)
	}
}

// leafConfig is one local OpenAI-shaped backend, "leaf", at baseURL serving chat and
// embeddings.
func leafConfig(baseURL string) config.RouterConfig {
	return config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"leaf": {ID: "leaf", BaseURL: baseURL, Capabilities: []config.Capability{config.CapChat, config.CapEmbed}},
		},
	}
}

// countingLeaf is a real Bifrost client over one leaf whose hits are counted. The
// leaf answers chat streams with one chunk.
func countingLeaf(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	client, err := NewClient(t.Context(), leafConfig(srv.URL), WithLogger(testLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client, &hits
}

const chatTestBody = `{"model":"m","messages":[{"role":"user","content":"hi"}]}`

func TestChatRawWithoutTimeoutFailsClosed(t *testing.T) {
	client, hits := countingLeaf(t)
	_, err := client.ChatCompletionRaw(t.Context(), Call{BackendID: "leaf", Model: "m", Body: []byte(chatTestBody)})
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("a chat with no timeout and no deadline must fail closed, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestEmbeddingRawWithoutTimeoutFailsClosed(t *testing.T) {
	client, hits := countingLeaf(t)
	_, err := client.EmbeddingRaw(t.Context(), Call{BackendID: "leaf", Model: "e", Body: []byte(`{"model":"e","input":"hi"}`)})
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("embeddings with no timeout and no deadline must fail closed, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestChatRawWithACallerDeadlineNeedsNoTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := requireCallBound(ctx, 0, "router.test"); err != nil {
		t.Fatalf("a caller deadline bounds the call: %v", err)
	}
	if err := requireCallBound(t.Context(), time.Second, "router.test"); err != nil {
		t.Fatalf("a timeout bounds the call: %v", err)
	}
	var nilCtx context.Context // Nil on purpose.
	if err := requireCallBound(nilCtx, time.Second, "router.test"); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a nil context is CodeInvalid, got %v", err)
	}
}

func TestChatStreamWithoutTimeoutAllowed(t *testing.T) {
	client, hits := countingLeaf(t)
	chunks, errs, err := client.ChatCompletionStreamRaw(t.Context(), Call{BackendID: "leaf", Model: "m", Body: []byte(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)})
	if err != nil {
		t.Fatalf("a stream is bounded by the gateway's watchdog, not a timeout: %v", err)
	}
	n := 0
	for range chunks {
		n++
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if n == 0 || hits.Load() != 1 {
		t.Fatalf("chunks=%d hits=%d", n, hits.Load())
	}
}

// switchyardClient is a real Bifrost client with Switchyard registered at the test
// server h, beside an unused leaf.
func switchyardClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	t.Setenv("POLYPUS_SWITCHYARD", "1")
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := leafConfig("http://127.0.0.1:9")
	cfg.Switchyard = config.SwitchyardConfig{BaseURL: srv.URL}
	client, err := NewClient(t.Context(), cfg, WithLogger(testLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if !client.UsesBifrost(ProviderSwitchyard) {
		t.Fatal("Switchyard must be registered")
	}
	return client
}

func TestSwitchyardProviderStreamAgainstHTTPTest(t *testing.T) {
	var gotPath, gotModel string
	client := switchyardClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotModel = body.Model
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Model-Router-Selected-Model", "leaf/b")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"leaf/b\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	var header http.Header
	chunks, errs, err := client.ChatCompletionStreamRaw(t.Context(), Call{
		BackendID:        ProviderSwitchyard,
		Model:            "investigator",
		Body:             []byte(`{"model":"router/investigator","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		OnResponseHeader: func(h http.Header) { header = h },
	})
	if err != nil {
		t.Fatal(err)
	}
	// The hook runs before the first chunk is handed over.
	if got := header.Get("X-Model-Router-Selected-Model"); got != "leaf/b" {
		t.Fatalf("the selected-model header must survive to the caller: %v", header)
	}
	var got []ChatStreamChunk
	for c := range chunks {
		got = append(got, c)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || !strings.Contains(string(got[0].OpenAIJSON), `"hi"`) {
		t.Fatalf("chunks %+v", got)
	}
	if gotPath != "/v1/chat/completions" || gotModel != "router/investigator" {
		t.Fatalf("Switchyard must get the client's body on the chat path: %s %s", gotPath, gotModel)
	}
}

func TestSwitchyardClientErrorIsCallerVisible(t *testing.T) {
	client := switchyardClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"unknown router","type":"invalid_request_error"}}`)
	})
	_, err := client.ChatCompletionRaw(t.Context(), Call{BackendID: ProviderSwitchyard, Model: "investigator", Body: []byte(chatTestBody), Timeout: 5 * time.Second})
	if derrors.CodeOf(err) != derrors.CodeInvalid || derrors.HTTPStatus(err) != http.StatusBadRequest {
		t.Fatalf("a Switchyard 400 is the caller's error, got %v", err)
	}
}

func TestSwitchyardRateLimitKeepsRetryAfter(t *testing.T) {
	client := switchyardClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_error"}}`)
	})
	_, err := client.ChatCompletionRaw(t.Context(), Call{BackendID: ProviderSwitchyard, Model: "investigator", Body: []byte(chatTestBody), Timeout: 5 * time.Second})
	if derrors.CodeOf(err) != derrors.CodeRateLimited || derrors.Field(err, ratelimit.FieldRetryAfter) != "9" {
		t.Fatalf("a Switchyard 429 keeps its Retry-After, got %v", err)
	}
}

func TestRegistryOnANilClientIsNil(t *testing.T) {
	var c *Client
	if c.Registry() != nil {
		t.Fatal("a nil client has no registry")
	}
}

func TestNewClientNeedsTheCloudflareGetterForACloudflareBackend(t *testing.T) {
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Remote:       true,
				Extension:    config.ExtensionCloudflare,
				BaseURL:      "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
				Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{config.CapTTS},
			},
		},
	}
	_, err := NewClient(t.Context(), cfg, WithLogger(testLogger()))
	if derrors.CodeOf(err) != derrors.CodeInvalid || !strings.Contains(err.Error(), "WithCloudflareClientGet") {
		t.Fatalf("a Cloudflare backend without a getter must fail closed, got %v", err)
	}
}

func TestChatStreamWithoutAContextFailsClosed(t *testing.T) {
	client, hits := countingLeaf(t)
	var ctx context.Context // Nil on purpose.
	_, _, err := client.ChatCompletionStreamRaw(ctx, Call{BackendID: "leaf", Model: "m", Body: []byte(chatTestBody)})
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("a stream with no context must fail closed, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("nothing may be sent")
	}
}

func TestContextCodeSplitsDeadlineFromCancel(t *testing.T) {
	if got := contextCode(context.DeadlineExceeded); got != derrors.CodeTimeout {
		t.Fatalf("deadline: %s", got)
	}
	if got := contextCode(context.Canceled); got != derrors.CodeCanceled {
		t.Fatalf("cancel: %s", got)
	}
}

func TestUsesBifrostKeepsTheSwitchyardItRegistered(t *testing.T) {
	client := switchyardClient(t, func(http.ResponseWriter, *http.Request) {})
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	if !client.UsesBifrost(ProviderSwitchyard) {
		t.Fatal("routing must follow what NewClient registered, not the environment now")
	}
}
