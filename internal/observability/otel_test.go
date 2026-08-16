package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestWrapTransportInjectsTraceparent(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	var gotParent string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotParent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	client := &http.Client{Transport: WrapTransport(http.DefaultTransport), Timeout: 5 * time.Second}
	ctx, span := tp.Tracer("test").Start(context.Background(), "client")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	span.End()

	if gotParent == "" {
		t.Fatal("expected traceparent on outbound request")
	}
	if !trace.SpanContextFromContext(ctx).IsValid() {
		t.Fatal("expected valid span context")
	}
}

func TestWrapHandlerSkipsHealth(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := wrapHandler(inner, []string{"/health"})

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	var names []string
	sawHealth := false
	sawChat := false
	for _, sp := range exporter.GetSpans() {
		names = append(names, sp.Name)
		if sp.Name == "GET /health" {
			sawHealth = true
		}
		if sp.Name == "polypus SERVER POST /v1/chat/completions" {
			sawChat = true
			kind, ok := attrString(sp, "openinference.span.kind")
			if !ok || kind != "CHAIN" {
				t.Fatalf("http span kind=%q want CHAIN", kind)
			}
			ioDir, ok := attrString(sp, "http.io")
			if !ok || ioDir != "server" {
				t.Fatalf("http.io=%q want server", ioDir)
			}
		}
	}
	if sawHealth {
		t.Fatalf("health probe should not create a span, got %v", names)
	}
	if !sawChat {
		t.Fatalf("missing chat span in %v", names)
	}
}

func attrString(sp tracetest.SpanStub, key string) (string, bool) {
	for _, attr := range sp.Attributes {
		if string(attr.Key) == key {
			return attr.Value.AsString(), true
		}
	}
	return "", false
}

func attrBool(sp tracetest.SpanStub, key string) (bool, bool) {
	for _, attr := range sp.Attributes {
		if string(attr.Key) == key {
			return attr.Value.AsBool(), true
		}
	}
	return false, false
}

type timeoutRoundTripper struct{}

func (timeoutRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.DeadlineExceeded
}

func TestWrapTransportRecordsTimeout(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	client := &http.Client{Transport: WrapTransport(timeoutRoundTripper{})}
	ctx, span := tp.Tracer("test").Start(context.Background(), "parent")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:1323/v1/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	span.End()

	found := false
	for _, sp := range exporter.GetSpans() {
		if sp.Name != "polypus CLIENT POST 127.0.0.1:1323/v1/chat/completions" {
			continue
		}
		if sp.Status.Code != codes.Error {
			t.Fatalf("http span status=%v", sp.Status.Code)
		}
		timeout, ok := attrBool(sp, "timeout")
		if !ok || !timeout {
			t.Fatal("expected timeout=true")
		}
		errType, _ := attrString(sp, "error.class")
		if errType != "timeout" {
			t.Fatalf("error.class=%q", errType)
		}
		ioDir, _ := attrString(sp, "http.io")
		if ioDir != "client" {
			t.Fatalf("http.io=%q want client", ioDir)
		}
		found = true
	}
	if !found {
		t.Fatal("missing timeout HTTP span")
	}
}

func TestEndSpanRecordsTimeout(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, span := tp.Tracer("test").Start(context.Background(), "polypus.chat")
	EndSpan(span, context.DeadlineExceeded)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	sp := spans[0]
	if sp.Status.Code != codes.Error {
		t.Fatalf("status=%v", sp.Status.Code)
	}
	timeout, ok := attrBool(sp, "timeout")
	if !ok || !timeout {
		t.Fatal("expected timeout=true")
	}
}

func TestFailureDumpWritesOnServerError(t *testing.T) {
	dir := t.TempDir()
	processor := newFailureDumpProcessor(dir, 0, 0)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "POST /v1/chat/completions", trace.WithSpanKind(trace.SpanKindServer))
	_, child := tp.Tracer("test").Start(ctx, "polypus.chat")
	child.SetStatus(codes.Error, "backend timeout")
	child.End()
	span.SetStatus(codes.Error, "502")
	span.End()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dump files=%d", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var doc dumpedTrace
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Reason != "request_span_error" {
		t.Fatalf("reason=%s", doc.Reason)
	}
	if doc.SpanCount != 2 {
		t.Fatalf("spans=%d", doc.SpanCount)
	}
}

func TestFailureDumpSkipsSuccessfulRequest(t *testing.T) {
	dir := t.TempDir()
	processor := newFailureDumpProcessor(dir, 0, 0)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	_, span := tp.Tracer("test").Start(context.Background(), "GET /health", trace.WithSpanKind(trace.SpanKindServer))
	span.SetStatus(codes.Ok, "")
	span.End()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected dump files=%d", len(entries))
	}
}
