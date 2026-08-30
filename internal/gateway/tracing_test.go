package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
)

func TestChatCompletionsPropagatesTraceparent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}))

	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"@cf/allow-me","object":"model"}]}`))
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
default_tts_backend: cf_local
default_stt_backend: cf_local
default_proxy_backend: cf_local
default_chat_backend: cf_local
default_vision_backend: cf_local
backends:
  cf_local:
    base_url: %s
    capabilities: [chat, vision, tts, stt, voices]
    models:
      allow:
        - "@cf/allow-me"
`, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("POLYPUS_MODELS_CACHE", filepath.Join(dir, "cache.json"))
	t.Setenv("POLYPUS_DEFAULT_MODEL", "")
	t.Setenv("POLYPUS_DEFAULT_STT_MODEL", "")

	inner, err := NewHandler(config.ServeOptions{BackendURL: cf.URL})
	if err != nil {
		t.Fatal(err)
	}
	handler := observability.WrapHandler(inner)

	ctx, root := tp.Tracer("client").Start(context.Background(), "Predict.process_evaluator-FeedbackAnalysis.v1")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(
		`{"model":"@cf/allow-me","messages":[{"role":"user","content":"hi"}]}`,
	))
	req = req.WithContext(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	root.End()

	if rec.Code != http.StatusOK {
		body, _ := io.ReadAll(rec.Body)
		t.Fatalf("status=%d body=%s", rec.Code, body)
	}
	// Bifrost owns the leaf HTTP client; W3C injection onto that hop is not wired yet.
	// Require the gateway LLM span to stay on the client trace.
	clientSC := trace.SpanContextFromContext(ctx)
	spans := exporter.GetSpans()
	var sawChat bool
	for _, sp := range spans {
		if sp.Name == "polypus.chat" {
			sawChat = true
			if sp.SpanContext.TraceID().String() != clientSC.TraceID().String() {
				t.Fatalf("polypus.chat trace %s != client %s", sp.SpanContext.TraceID(), clientSC.TraceID())
			}
		}
	}
	if !sawChat {
		t.Fatalf("missing polypus.chat span in %+v", spanNames(spans))
	}
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, 0, len(spans))
	for _, sp := range spans {
		names = append(names, sp.Name)
	}
	return names
}
