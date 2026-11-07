package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

func TestOversizedChatRequestGets413(t *testing.T) {
	h, _ := newOpenRouterBatchGateway(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("a", chatMaxBody+1)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413", rec.Code)
	}
}

// postChat sends one chat request for model to h.
func postChat(t *testing.T, h http.Handler, model string, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model":    model,
		"stream":   stream,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestChatSendsTheCallToTheRouterOnce(t *testing.T) {
	sr := &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}]}`), nil
	}}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	rec := postChat(t, h, "leaf/m", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	calls := sr.recorded()
	if len(calls) != 1 || calls[0].BackendID != "leaf" || calls[0].Model != "m" {
		t.Fatalf("a paid chat must reach the router once for its backend: %+v", calls)
	}
	if calls[0].Timeout <= 0 {
		t.Fatal("a non-stream chat must carry its hop budget")
	}
}

func TestChatUpstreamFailureMapsTo502(t *testing.T) {
	sr := &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return nil, derrors.New(derrors.CodeUnavailable, "router.bifrost", "provider call").With(ratelimit.FieldStatus, "503")
	}}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	rec := postChat(t, h, "leaf/m", false)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("an upstream failure is a 502, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestChatClientErrorPassesThroughAndKeepsTheBreakerClosed(t *testing.T) {
	sr := &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return nil, derrors.New(derrors.CodeInvalid, "router.anthropic", "bad request").With(ratelimit.FieldStatus, "400")
	}}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	attempts := int(upstream.ConsecutiveFailuresTrip) + 2
	for i := range attempts {
		rec := postChat(t, h, "leaf/m", false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: a 4xx reaches the caller as a 4xx, got %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if got := len(sr.recorded()); got != attempts {
		t.Fatalf("a 4xx must not open the breaker: router calls=%d", got)
	}
}

func TestChatRateLimitKeepsRetryAfter(t *testing.T) {
	sr := &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return nil, rateLimited("7")
	}}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	rec := postChat(t, h, "leaf/m", false)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "7" {
		t.Fatalf("a 429 must reach the caller with Retry-After: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
}

func TestChatStreamWithoutAFirstChunkHitsTheHop(t *testing.T) {
	sr := &scriptedRouter{stream: stallingStream()}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	start := time.Now()
	rec := postChat(t, h, "leaf/m", true)
	if rec.Code != http.StatusGatewayTimeout || time.Since(start) > 3*time.Second {
		t.Fatalf("a stream with no first chunk must fail at the hop: %d after %s, %s", rec.Code, time.Since(start), rec.Body.String())
	}
}

func TestChatStreamThatStallsIsCutOff(t *testing.T) {
	sr := &scriptedRouter{stream: stallingStream(`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"}}]}`)}
	cfg := scriptedRouterConfig()
	cfg.Timeouts.Chat = 2 * time.Second
	h := newScriptedGateway(t, sr, cfg)
	start := time.Now()
	rec := postChat(t, h, "leaf/m", true)
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("a stalled stream must be cut off at the idle limit, took %s", elapsed)
	}
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("the chunks before the stall must reach the client: %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "[DONE]") {
		t.Fatalf("a cut-off stream must not end with DONE: %q", rec.Body.String())
	}
}

func TestChatStreamDumpsADroppedChunk(t *testing.T) {
	dir := t.TempDir()
	sr := &scriptedRouter{stream: streamOf(
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
		`{"id":"1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"late"}}]}`,
	)}
	h := newScriptedGateway(t, sr, scriptedRouterConfig(), WithObservability(dir, nil))
	rec := postChat(t, h, "leaf/m", true)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "late") {
		t.Fatalf("a chunk after finish_reason must be dropped: %d %q", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Name(), chunkDumpInfix) {
		t.Fatalf("the dropped chunk must be dumped: %v %v", entries, err)
	}
}

func TestChatUnregisteredBackendFails(t *testing.T) {
	for _, stream := range []bool{false, true} {
		sr := &scriptedRouter{unregistered: map[string]bool{"leaf": true}}
		h := newScriptedGateway(t, sr, scriptedRouterConfig())
		rec := postChat(t, h, "leaf/m", stream)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), string(derrors.CodeFailedPrecondition)) {
			t.Fatalf("stream=%v: an unregistered backend fails closed: %d %s", stream, rec.Code, rec.Body.String())
		}
		if len(sr.recorded()) != 0 {
			t.Fatalf("stream=%v: nothing may be dialled", stream)
		}
	}
}

func TestEmbeddingsUnregisteredBackendFails(t *testing.T) {
	sr := &scriptedRouter{unregistered: map[string]bool{"leaf": true}}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	rec := postEmbeddings(t, h)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), string(derrors.CodeFailedPrecondition)) {
		t.Fatalf("an unregistered backend fails closed: %d %s", rec.Code, rec.Body.String())
	}
	if len(sr.recorded()) != 0 {
		t.Fatal("nothing may be dialled")
	}
}

func TestEmbeddingsGoThroughTheRouterWithABudget(t *testing.T) {
	sr := &scriptedRouter{embed: func(context.Context, router.Call) ([]byte, error) {
		return []byte(`{"data":[{"embedding":[0.1]}]}`), nil
	}}
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	rec := postEmbeddings(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	calls := sr.recorded()
	if len(calls) != 1 || calls[0].Model != "e" || calls[0].Timeout <= 0 {
		t.Fatalf("embeddings must reach the router once with a budget: %+v", calls)
	}
}

// postEmbeddings sends one embeddings request for leaf/e to h.
func postEmbeddings(t *testing.T, h http.Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"leaf/e","input":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rateLimited is the error the router returns for a provider 429 with Retry-After.
func rateLimited(retryAfter string) error {
	rl, _ := ratelimit.ClassifyStatus("router.bifrost", http.StatusTooManyRequests, http.Header{"Retry-After": {retryAfter}}, nil)
	return derrors.Wrap(rl, derrors.CodeRateLimited, "router.bifrost", "provider rate limited")
}

func TestChatDefaultBackendMissingIsNotReady(t *testing.T) {
	cfg := scriptedRouterConfig()
	cfg.Chat.Default = "ghost"
	h := newScriptedGateway(t, &scriptedRouter{}, cfg)
	rec := postChat(t, h, "m", false)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), string(derrors.CodeNotReady)) {
		t.Fatalf("a default backend missing from config is the gateway's fault (503), not the caller's: %d %s", rec.Code, rec.Body.String())
	}
}
