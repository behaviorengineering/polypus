package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sony/gobreaker"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

func TestChatRequestKeepsLargeIntegersExact(t *testing.T) {
	const seed = "9007199254740993" // 2^53 + 1: a float64 round trip changes it.
	req, err := parseChatRequest([]byte(`{"model":"m","seed":` + seed + `,"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := req.outbound("down", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"seed":`+seed) {
		t.Fatalf("seed changed on re-encode: %s", out)
	}
}

func TestReasoningMaxTokensMatchesDecodedNumber(t *testing.T) {
	root := map[string]any{keyReasoning: map[string]any{keyMaxTokens: json.Number("-1")}}
	if setNestedReasoningMaxTokens(root, -1) {
		t.Fatal("an equal json.Number must not count as a change")
	}
}

func TestSystemOneModelRewriteKeepsLargeIntegersExact(t *testing.T) {
	out, err := rewriteSystemOneResponseModel([]byte(`{"model":"x","n":9007199254740993}`), "public")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"n":9007199254740993`) || !strings.Contains(string(out), `"model":"public"`) {
		t.Fatalf("rewrite: %s", out)
	}
}

func TestReasoningEffortIgnoresNonStrings(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"reasoning":{"effort":7}}`, ""},
		{`{"reasoning_effort":1}`, ""},
		{`{"reasoning":{"effort":null},"reasoning_effort":"high"}`, "high"},
		{`{"reasoning":{"effort":" low "}}`, "low"},
	}
	for _, c := range cases {
		var root map[string]any
		if err := json.Unmarshal([]byte(c.body), &root); err != nil {
			t.Fatal(err)
		}
		if got := chatMapReasoningEffort(root); got != c.want {
			t.Fatalf("%s: effort %q want %q", c.body, got, c.want)
		}
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(`{"messages":[{"content":[{"type":7}]}]}`), &root); err != nil {
		t.Fatal(err)
	}
	if chatMapHasVision(root) {
		t.Fatal("a non-string part type is not vision")
	}
}

func TestRouteWithUnknownMethodSetFailsClosed(t *testing.T) {
	rt := gatewayRoute{path: "/x", methods: routeMethods(99)}
	if rt.matches(httptest.NewRequest(http.MethodGet, "/x", nil)) {
		t.Fatal("an unknown method set must match nothing")
	}
}

func TestBreakerRefusalSendsOnlyThePublicMessage(t *testing.T) {
	var logs bytes.Buffer
	s := &shared{log: slog.New(slog.NewTextHandler(&logs, nil))}
	err := derrors.Wrap(upstream.UnavailableError("leaf", gobreaker.ErrOpenState), derrors.CodeUnavailable, "gateway.secretOp", "dial")
	rec := httptest.NewRecorder()
	s.writeHandlerError(rec, err)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "gateway.secretOp") || strings.Contains(body, "upstream.Execute") {
		t.Fatalf("op chain leaked to the client: %s", body)
	}
	if !strings.Contains(logs.String(), "gateway.secretOp") {
		t.Fatalf("the full chain must reach the log: %s", logs.String())
	}
}

func TestAdminAllowOversizedBodyIs413(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:1/v1"))
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, &fakeRouter{reg: reg}, WithAdminStateDir(t.TempDir()))
	body := `{"backend":"cf","model":"` + strings.Repeat("x", adminAllowMaxBody) + `"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, pathAdminModelsAllow, strings.NewReader(body)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestAdminAllowUnknownBackendUsesTheDomainCode(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:1/v1"))
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, &fakeRouter{reg: reg}, WithAdminStateDir(t.TempDir()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, pathAdminModelsAllow, strings.NewReader(`{"backend":"nope","model":"m"}`)))
	var got openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || got.Error.Code != string(derrors.CodeNotFound) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSwitchyardConfigWithoutRouterIsTyped(t *testing.T) {
	if code := derrors.CodeOf(writeSwitchyardConfig(nil, config.ServeOptions{})); code != derrors.CodeNotReady {
		t.Fatalf("code %q", code)
	}
}

func TestWrongMethodOnV1RoutesIs405WithAllow(t *testing.T) {
	h := newBatchGatewayHandler(t, nil)
	cases := []struct {
		method, path, allow string
	}{
		{http.MethodPut, pathBatches, "GET, POST"},
		{http.MethodGet, pathBatchesPrefix + "batch_x/cancel", "POST"},
		{http.MethodGet, pathFiles, "POST"},
		{http.MethodDelete, pathAdminModelsAllow, "GET, HEAD, POST"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != c.allow {
			t.Fatalf("%s %s: status %d Allow %q", c.method, c.path, rec.Code, rec.Header().Get("Allow"))
		}
		if !strings.Contains(rec.Body.String(), codeMethodNotAllowed) {
			t.Fatalf("%s %s: want an OpenAI error body, got %s", c.method, c.path, rec.Body.String())
		}
	}
}

func TestModelRetrieveBadEscapeIsJSON(t *testing.T) {
	h := newBatchGatewayHandler(t, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, pathModelsPrefix+"%25zz", nil))
	if rec.Code != http.StatusBadRequest || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("status %d content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestBatchCancelForARemovedBackendFailsBeforeTheProvider(t *testing.T) {
	h := newBatchGatewayHandler(t, nil)
	meta := &batch.Meta{BackendID: "removed", Status: batch.StatusInProgress, Endpoint: pathChatCompletions}
	if err := h.batchStore.SaveBatch(meta); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, pathBatchesPrefix+meta.ID+"/cancel", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "backend not found") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSystemOneFallbackBackendIsDeterministic(t *testing.T) {
	cfg := config.RouterConfig{Backends: map[string]config.BackendDef{}}
	for _, id := range []string{"zeta", "alpha", "mid", "beta"} {
		cfg.Backends[id] = config.BackendDef{ID: id, Capabilities: []config.Capability{config.CapSystemOne}}
	}
	for range 50 {
		if id, _ := backendAndDownstreamForPublicID(cfg, "bare-model"); id != "alpha" {
			t.Fatalf("picked %q, want the first sorted id", id)
		}
	}
}

func TestStreamChunkDumpNameUsesTheGatewayClock(t *testing.T) {
	dir := t.TempDir()
	at := time.Unix(1_700_000_000, 42)
	ctx := withStreamChunkCounter(context.Background(), chunkDumpSink{dir: dir, log: testShared.log, now: func() time.Time { return at }})
	dumpIllegalStreamChunk(ctx, 1, []byte(`{"x":1}`), nil)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("dumps %v err %v", entries, err)
	}
	if want := strconv.FormatInt(at.UnixNano(), 10) + ".json"; !strings.HasSuffix(entries[0].Name(), want) {
		t.Fatalf("name %q want suffix %q", entries[0].Name(), want)
	}
}

func TestModelsCacheStampsWithTheInjectedClock(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	now := at
	c := &modelsInventoryCache{byKey: map[string]modelsCacheEntry{}, ttl: time.Minute, log: testShared.log, now: func() time.Time { return now }}
	c.put("b", []openaiModel{{ID: "b/m"}})
	if got := c.byKey["b"].At; !got.Equal(at) {
		t.Fatalf("stamped %s want %s", got, at)
	}
	now = at.Add(2 * time.Minute)
	if _, ok := c.get("b"); ok {
		t.Fatal("the entry must expire on the injected clock")
	}
}

func TestModelsCacheConstructorNeedsAClock(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic for a nil clock")
		}
	}()
	newModelsInventoryCache(testShared.log, time.Minute, nil)
}

func TestModelsRefreshLoopWaitReturnsAfterCancel(t *testing.T) {
	h := modelsHandler{&shared{log: testShared.log, models: config.ModelsInventory{Warmup: true, RefreshInterval: time.Hour}}}
	ctx, cancel := context.WithCancel(context.Background())
	wait := h.startModelsRefreshLoop(ctx, config.RouterConfig{})
	cancel()
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("wait must return once the loop stops")
	}
}

// stallingRouter streams Bifrost chat chunks it is fed, then stalls until cancelled.
type stallingRouter struct {
	*fakeRouter
	first []router.ChatStreamChunk
}

func (s *stallingRouter) UsesBifrost(string) bool { return true }

func (s *stallingRouter) ChatCompletionStreamRaw(ctx context.Context, _ router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	chunks := make(chan router.ChatStreamChunk)
	errCh := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errCh)
		for _, c := range s.first {
			select {
			case chunks <- c:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
		errCh <- derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "fake.stream", "context done")
	}()
	return chunks, errCh, nil
}

func stallingChatHandler(r *stallingRouter) chatHandler {
	return chatHandler{&shared{
		routerParts: routerParts{chat: r},
		log:         testShared.log,
		clock:       time.Now,
		upstreams:   upstream.NewBoard(),
		timeouts:    config.Timeouts{Min: time.Millisecond, Max: time.Second, StreamIdle: 100 * time.Millisecond},
	}}
}

func TestBifrostStreamWithoutAFirstChunkHitsTheHop(t *testing.T) {
	h := stallingChatHandler(&stallingRouter{})
	start := time.Now()
	err := h.bifrostChatStream(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil),
		chatCall{target: backendTarget{id: "leaf"}, stream: true, hop: 100 * time.Millisecond})
	if derrors.CodeOf(err) != derrors.CodeTimeout || time.Since(start) > 3*time.Second {
		t.Fatalf("want a timeout at the hop: err=%v after %s", err, time.Since(start))
	}
}

func TestBifrostStreamThatStallsIsCutOffAndCountsAgainstTheBackend(t *testing.T) {
	h := stallingChatHandler(&stallingRouter{first: []router.ChatStreamChunk{
		{OpenAIJSON: []byte(`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`)},
	}})
	rec := httptest.NewRecorder()
	start := time.Now()
	err := h.bifrostChatStream(rec, httptest.NewRequest(http.MethodPost, "/", nil),
		chatCall{target: backendTarget{id: "leaf"}, stream: true, hop: 5 * time.Second})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("a stalled stream must be cut off at the idle limit: err=%v after %s", err, time.Since(start))
	}
	if derrors.CodeOf(err) != derrors.CodeTimeout || !errors.Is(err, upstream.ErrResponseWritten) {
		t.Fatalf("want a timeout after headers, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("the chunk before the stall must reach the client: %q", rec.Body.String())
	}
}
