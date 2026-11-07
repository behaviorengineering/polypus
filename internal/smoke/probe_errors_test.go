package smoke

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func probeCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCallRejectsABodyOverTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 65)))
	}))
	t.Cleanup(srv.Close)
	req, err := httpNewGet(probeCtx(t), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := call(req, "speech", 64); err == nil || !strings.Contains(err.Error(), "speech: body over 64 bytes") {
		t.Fatalf("err = %v, want an over-limit error rather than a cut body", err)
	}
	req, _ = httpNewGet(probeCtx(t), srv.URL)
	if raw, _, err := call(req, "speech", 65); err != nil || len(raw) != 65 {
		t.Fatalf("a body at the limit must pass: %d bytes, %v", len(raw), err)
	}
}

func TestProbeDecodeErrorsNameTheCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	t.Cleanup(srv.Close)
	ctx := probeCtx(t)
	for name, run := range map[string]func() error{
		"files upload: decode":   func() error { _, err := uploadBatchJSONL(ctx, srv.URL, "{}\n"); return err },
		"batches create: decode": func() error { _, err := createBatch(ctx, srv.URL, "file-1"); return err },
		"batches get: decode":    func() error { _, err := getBatch(ctx, srv.URL, "b1"); return err },
		"systemone: decode":      func() error { _, err := systemOnePing(ctx, Options{BaseURL: srv.URL}); return err },
		"transcription: decode":  func() error { _, err := transcribeAudio(ctx, Options{BaseURL: srv.URL}, []byte("mp3")); return err },
		"chat: decode":           func() error { _, err := chatPing(ctx, Options{BaseURL: srv.URL}, "m"); return err },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestChatPingTransportErrorNamesTheCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close()
	if _, err := chatPing(probeCtx(t), Options{BaseURL: base}, "m"); err == nil || !strings.HasPrefix(err.Error(), "chat: ") {
		t.Fatalf("err = %v, want the chat prefix", err)
	}
}

func TestPingSendsReasoningEffortWhenSet(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := chatPing(probeCtx(t), Options{BaseURL: srv.URL, ReasoningEffort: "low"}, "m"); err != nil {
		t.Fatal(err)
	}
	if _, err := chatPing(probeCtx(t), Options{BaseURL: srv.URL}, "m"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bodies[0], `"reasoning":{"effort":"low"}`) {
		t.Fatalf("body %s, want reasoning.effort", bodies[0])
	}
	if strings.Contains(bodies[1], "reasoning") {
		t.Fatalf("body %s, want no reasoning when unset", bodies[1])
	}
}

func TestChannelSpanCarriesTheProbeCause(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}))
	t.Cleanup(srv.Close)
	opts := Options{BaseURL: srv.URL, ChatModel: "m"}
	if _, err := runChannel(probeCtx(t), ChannelChat, opts); err == nil {
		t.Fatal("want the chat channel to fail")
	}
	spans := exporter.GetSpans()
	if len(spans) == 0 {
		t.Fatal("no channel span")
	}
	ch := spans[len(spans)-1]
	if ch.Status.Code != codes.Error || !strings.Contains(ch.Status.Description, "status 418") || !strings.Contains(ch.Status.Description, "content_nonempty") {
		t.Fatalf("channel span status = %+v, want the probe and its status", ch.Status)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(ok.Close)
	exporter.Reset()
	if _, err := runChannel(probeCtx(t), ChannelChat, Options{BaseURL: ok.URL, ChatModel: "m"}); err != nil {
		t.Fatal(err)
	}
	spans = exporter.GetSpans()
	if last := spans[len(spans)-1]; last.Status.Code != codes.Ok {
		t.Fatalf("a passing channel span = %+v, want Ok", last.Status)
	}
}

func TestChatEmptyContentShowsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning":"thinking only"}}]}`))
	}))
	t.Cleanup(srv.Close)
	results, err := runChat(probeCtx(t), Options{BaseURL: srv.URL, ChatModel: "m"})
	if err == nil {
		t.Fatal("runChat succeeded, want a content_nonempty failure")
	}
	var detail string
	for _, r := range results {
		if r.Probe == "content_nonempty" {
			detail = r.Detail
		}
	}
	if !strings.HasPrefix(detail, "empty content: ") || !strings.Contains(detail, "thinking only") {
		t.Fatalf("content_nonempty detail = %q, want the empty-content error with the body", detail)
	}
}
