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
	var iface schemas.Tracer = tr

	bctx := schemas.NewBifrostContext(parentCtx, schemas.NoDeadline)
	_, handle := iface.StartSpanID(bctx, "chat_completion_stream test-model", schemas.SpanKindLLMCall)
	bridge, ok := handle.(*otelSpanBridge)
	if !ok || bridge == nil {
		t.Fatal("expected otel span bridge handle")
	}
	bridge.schema.SetAttribute(schemas.AttrBifrostProviderName, string(schemas.Gemini))
	iface.EndSpan(handle, schemas.SpanStatusOk, "")
	if n := handleCount(tr); n != 0 {
		t.Fatalf("EndSpan through schemas.Tracer left %d handles", n)
	}
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

func TestBifrostOTelTracer_concurrentRequestsKeepTheirOwnTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	ctxA, spanA := Tracer().Start(context.Background(), "polypus.chat A")
	ctxB, spanB := Tracer().Start(context.Background(), "polypus.chat B")
	// Interleave like two concurrent requests on the one shared tracer.
	_, hA := iface.StartSpanID(schemas.NewBifrostContext(ctxA, schemas.NoDeadline), "llm A", schemas.SpanKindLLMCall)
	_, hB := iface.StartSpanID(schemas.NewBifrostContext(ctxB, schemas.NoDeadline), "llm B", schemas.SpanKindLLMCall)
	iface.EndSpan(hA, schemas.SpanStatusOk, "")
	iface.EndSpan(hB, schemas.SpanStatusOk, "")
	spanA.End()
	spanB.End()
	if n := handleCount(tr); n != 0 {
		t.Fatalf("EndSpan through schemas.Tracer left %d handles", n)
	}
	want := map[string]string{
		"llm A": spanA.SpanContext().TraceID().String(),
		"llm B": spanB.SpanContext().TraceID().String(),
	}
	exported := 0
	for _, sp := range exporter.GetSpans() {
		w, ok := want[sp.Name]
		if !ok {
			continue
		}
		exported++
		if sp.SpanContext.TraceID().String() != w {
			t.Fatalf("%s joined trace %s, want its own request's %s", sp.Name, sp.SpanContext.TraceID(), w)
		}
	}
	if exported != len(want) {
		t.Fatalf("exported %d bifrost spans, want %d", exported, len(want))
	}
	if got := tr.CreateTrace(""); len(got) != 32 {
		t.Fatalf("CreateTrace with no parent must return a fresh 32-hex id, got %q", got)
	}
}

func TestBifrostOTelTracer_EndSpanThroughInterfaceExportsAndReleases(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	tr := NewBifrostOTelTracer()
	var iface schemas.Tracer = tr
	bctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	_, handle := iface.StartSpan(bctx, "llm", schemas.SpanKindLLMCall)
	if handle == nil {
		t.Fatal("StartSpan returned a nil handle")
	}
	if n := handleCount(tr); n != 1 {
		t.Fatalf("after StartSpan: %d handles, want 1", n)
	}
	iface.EndSpan(handle, schemas.SpanStatusError, "boom")
	if n := handleCount(tr); n != 0 {
		t.Fatalf("after EndSpan: %d handles, want 0", n)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "llm" {
		t.Fatalf("exported spans = %+v, want one \"llm\" span", spans)
	}
	if spans[0].Status.Description != "boom" {
		t.Fatalf("status = %+v, want error \"boom\"", spans[0].Status)
	}
}

func TestBifrostOTelTracer_StartSpanIDNilContextNoSpan(t *testing.T) {
	tr := NewBifrostOTelTracer()
	//nolint:staticcheck // SA1012: a nil ctx is the case under test.
	id, handle := tr.StartSpanID(nil, "llm", schemas.SpanKindLLMCall)
	if id != "" || handle != nil {
		t.Fatalf("nil ctx: got (%q, %v), want no span", id, handle)
	}
	if n := handleCount(tr); n != 0 {
		t.Fatalf("nil ctx stored %d handles", n)
	}
}

func handleCount(tr *BifrostOTelTracer) int {
	n := 0
	tr.handles.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}
