package observability

import (
	"context"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRecordInterventionSetsAttributeAndEvent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	ctx, span := Tracer().Start(context.Background(), "polypus.chat")
	ctx = WithInterventionState(ctx)
	RecordIntervention(ctx, InterventionReasoningAlias)
	RecordIntervention(ctx, InterventionSSEDrop)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	got, ok := interventionAttrString(spans[0], attrIntervention)
	if !ok || got != "reasoning_alias,sse_drop" {
		t.Fatalf("polypus.intervention = %q ok=%v", got, ok)
	}
	var events int
	for _, ev := range spans[0].Events {
		if ev.Name != "polypus.intervention" {
			continue
		}
		events++
		code, ok := interventionEventAttr(ev.Attributes, "polypus.intervention.code")
		if !ok {
			t.Fatalf("missing code on event")
		}
		if code != "reasoning_alias" && code != "sse_drop" {
			t.Fatalf("unexpected code %q", code)
		}
	}
	if events != 2 {
		t.Fatalf("expected 2 intervention events, got %d", events)
	}
}

func interventionEventAttr(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

func interventionAttrString(sp tracetest.SpanStub, key string) (string, bool) {
	for _, a := range sp.Attributes {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

func TestJoinInterventionsDedupes(t *testing.T) {
	got := JoinInterventions("reasoning_alias", InterventionReasoningAlias)
	if got != "reasoning_alias" {
		t.Fatalf("got %q", got)
	}
	got = JoinInterventions("reasoning_alias", InterventionSSEDrop)
	if got != "reasoning_alias,sse_drop" {
		t.Fatalf("got %q", got)
	}
}

func TestRecordInterventionFromTwoGoroutines(t *testing.T) {
	ctx := WithInterventionState(context.Background())
	var wg sync.WaitGroup
	for _, code := range []Intervention{InterventionSSEDrop, InterventionReasoningAlias} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				RecordIntervention(ctx, code)
			}
		}()
	}
	wg.Wait()
	got := InterventionJoined(ctx)
	if !strings.Contains(got, string(InterventionSSEDrop)) || !strings.Contains(got, string(InterventionReasoningAlias)) {
		t.Fatalf("joined = %q, want both codes", got)
	}
}
