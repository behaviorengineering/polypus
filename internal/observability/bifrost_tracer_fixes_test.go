package observability

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func memoryExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return exporter
}

func TestBifrostOTelTracer_unsetStatusStaysUnset(t *testing.T) {
	exporter := memoryExporter(t)
	tr := NewBifrostOTelTracer()
	_, handle := tr.StartSpanID(context.Background(), "llm", schemas.SpanKindLLMCall)
	tr.EndSpan(handle, schemas.SpanStatusUnset, "")
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Status.Code != codes.Unset {
		t.Fatalf("spans %+v, want one span with an unset status, not Ok", spans)
	}
}

func TestBifrostOTelTracer_eventAttributesAreRedacted(t *testing.T) {
	exporter := memoryExporter(t)
	tr := NewBifrostOTelTracer()
	_, handle := tr.StartSpanID(context.Background(), "agent", schemas.SpanKindLLMCall)
	tr.AddEvent(handle, "tool.call", map[string]any{
		"url.full": "https://example.test/v1?key=sk-event-secret",
		"attempt":  2,
	})
	tr.EndSpan(handle, schemas.SpanStatusOk, "")
	spans := exporter.GetSpans()
	if len(spans) != 1 || len(spans[0].Events) != 1 {
		t.Fatalf("spans %+v, want one span with one event", spans)
	}
	for _, a := range spans[0].Events[0].Attributes {
		if strings.Contains(a.Value.String(), "sk-event-secret") {
			t.Fatalf("event attribute %s leaked the key: %s", a.Key, a.Value.String())
		}
		if a.Key == "attempt" && a.Value.AsInt64() != 2 {
			t.Fatalf("attempt = %s, want the int kept", a.Value.String())
		}
	}
}

// TestBifrostOTelTracer_flushWhileBifrostWrites: Bifrost writes span attributes
// through SpanFromHandle under the span's lock while the tracer flushes them; the
// flush must not range the live map (go test -race, or Go's fatal concurrent map
// iteration and map write).
func TestBifrostOTelTracer_flushWhileBifrostWrites(t *testing.T) {
	memoryExporter(t)
	tr := NewBifrostOTelTracer()
	_, handle := tr.StartSpanID(context.Background(), "llm", schemas.SpanKindLLMCall)
	span := tr.SpanFromHandle(handle)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 2000 {
			span.SetAttribute(fmt.Sprintf("late.%d", i%50), i)
		}
	}()
	for range 200 {
		tr.PopulateLLMResponseAttributes(nil, handle, nil, nil)
	}
	wg.Wait()
	tr.EndSpan(handle, schemas.SpanStatusOk, "")
}

func TestBifrostOTelTracer_concurrentStreamChunks(t *testing.T) {
	tr := NewBifrostOTelTracer()
	const traceID = "race-stream"
	tr.CreateStreamAccumulator(traceID, time.Now())
	const workers = 32
	const perWorker = 50
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range perWorker {
				tr.AddStreamingChunk(traceID, &schemas.BifrostResponse{
					ChatResponse: &schemas.BifrostChatResponse{Model: "m"},
				})
			}
		}()
	}
	wg.Wait()
	_, _, n := tr.GetAccumulatedChunks(traceID)
	if n != workers*perWorker {
		t.Fatalf("chunks = %d, want %d", n, workers*perWorker)
	}
}

func TestBifrostOTelTracer_stopClearsDeferredAndStream(t *testing.T) {
	memoryExporter(t)
	tr := NewBifrostOTelTracer()
	_, handle := tr.StartSpanID(context.Background(), "llm", schemas.SpanKindLLMCall)
	const traceID = "stop-cleanup"
	tr.StoreDeferredSpan(traceID, handle)
	tr.CreateStreamAccumulator(traceID, time.Now())
	tr.AddStreamingChunk(traceID, &schemas.BifrostResponse{
		ChatResponse: &schemas.BifrostChatResponse{Model: "m"},
	})
	tr.Stop()
	if tr.GetDeferredSpanHandle(traceID) != nil {
		t.Fatal("deferred handle still present after Stop")
	}
	acc, _, n := tr.GetAccumulatedChunks(traceID)
	if acc != nil || n != 0 {
		t.Fatalf("stream accumulator still present after Stop: resp=%v chunks=%d", acc, n)
	}
	if n := handleCount(tr); n != 0 {
		t.Fatalf("Stop left %d handles", n)
	}
}
