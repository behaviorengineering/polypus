package observability

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestBifrostOTelTracer_sameTraceAsParent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	parentCtx, parent := Tracer().Start(context.Background(), "polypus.chat")
	parentTrace := parent.SpanContext().TraceID()

	tr := NewBifrostOTelTracer()
	tr.Bind(parentCtx)

	traceID := tr.CreateTrace("")
	if traceID != parentTrace.String() {
		t.Fatalf("CreateTrace %q != parent trace %q", traceID, parentTrace)
	}

	bctx := schemas.NewBifrostContext(parentCtx, schemas.NoDeadline)
	_, handle := tr.StartSpanID(bctx, "chat_completion_stream test-model", schemas.SpanKindLLMCall)
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil {
		t.Fatal("expected otel span bridge handle")
	}
	bridge.schema.SetAttribute(schemas.AttrBifrostProviderName, string(schemas.Gemini))
	tr.EndSpan(handle, schemas.SpanStatusOk, "")
	parent.End()

	var childTrace string
	for _, sp := range exporter.GetSpans() {
		if sp.Name == "chat_completion_stream test-model" {
			childTrace = sp.SpanContext.TraceID().String()
			for _, a := range sp.Attributes {
				if a.Key == attrURLFull {
					if !strings.Contains(a.Value.AsString(), "generativelanguage.googleapis.com") {
						t.Fatalf("url.full missing host: %s", a.Value.AsString())
					}
					if !strings.Contains(a.Value.AsString(), "key=REDACTED") {
						t.Fatalf("url.full not redacted: %s", a.Value.AsString())
					}
				}
			}
		}
	}
	if childTrace == "" {
		t.Fatal("missing bifrost child span export")
	}
	if childTrace != parentTrace.String() {
		t.Fatalf("child trace %q != parent %q", childTrace, parentTrace)
	}
}

func TestBifrostOTelTracer_gateSendPassthrough(t *testing.T) {
	tr := NewBifrostOTelTracer()
	ch := make(chan *schemas.BifrostStreamChunk, 1)
	chunk := &schemas.BifrostStreamChunk{}
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	ok := tr.GateSend("trace-1", chunk, false, false, ch, bctx)
	if !ok {
		t.Fatal("GateSend should forward via embedded NoOpTracer")
	}
	if len(ch) != 1 {
		t.Fatal("expected chunk on channel")
	}
}
