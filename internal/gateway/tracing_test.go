package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// TestChatCompletionsPropagatesTraceparent ensures inbound W3C context reaches
// polypus.chat and the Bifrost stream path stays on the same trace ID.
// Outbound Google HTTP is not wrapped in Bifrost v1.8.4; Gemini children are synthetic Bifrost spans.
func TestChatCompletionsPropagatesTraceparent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))

	dir := t.TempDir()
	writeConfig(t, dir, `
chat_backend:
  enabled: true
  default: leaf
tts_backend:
  enabled: true
  default: leaf
stt_backend:
  enabled: true
  default: leaf
backends:
  leaf:
    base_url: http://127.0.0.1:9
    capabilities: [chat, tts, stt, voices]
    models:
      allow:
        - test-model
`)
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
	handler := observability.WrapHandler(newTestGateway(t, opts, recRouter))

	ctx, root := tp.Tracer("client").Start(context.Background(), "test.client")
	rootTrace := root.SpanContext().TraceID()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"leaf/test-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
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

	var chatTrace string
	for _, sp := range exporter.GetSpans() {
		if sp.Name == "polypus.chat" {
			chatTrace = sp.SpanContext.TraceID().String()
		}
	}
	if chatTrace == "" {
		t.Fatal("missing polypus.chat span")
	}
	if chatTrace != rootTrace.String() {
		t.Fatalf("polypus.chat trace %q != client trace %q", chatTrace, rootTrace)
	}
}
