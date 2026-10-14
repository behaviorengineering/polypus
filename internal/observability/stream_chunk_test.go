package observability

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRecordChatStreamChunk_eventsOnActiveSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	ctx, span := Tracer().Start(context.Background(), "polypus.chat")
	raw := []byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	openai := []byte(`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`)
	RecordChatStreamChunk(ctx, 1, openai, raw, "forward", true)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	var saw bool
	for _, ev := range spans[0].Events {
		if ev.Name != "polypus.bifrost.stream_chunk" {
			continue
		}
		saw = true
		m := eventAttrMap(ev.Attributes)
		if m["polypus.stream.chunk_index"] != int64(1) {
			t.Fatalf("chunk_index: %v", m["polypus.stream.chunk_index"])
		}
		if m["polypus.stream.sse_action"] != "forward" {
			t.Fatalf("sse_action: %v", m["polypus.stream.sse_action"])
		}
		if m["polypus.stream.raw_present"] != true {
			t.Fatalf("raw_present: %v", m["polypus.stream.raw_present"])
		}
		if m["polypus.stream.openai_sha256"] == "" {
			t.Fatal("missing openai_sha256")
		}
	}
	if !saw {
		t.Fatal("missing polypus.bifrost.stream_chunk event")
	}
}

func TestRecordChatStreamChunk_debugRedactsSecrets(t *testing.T) {
	t.Setenv("POLYPUS_STREAM_CHUNK_DEBUG", "true")
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	ctx, span := Tracer().Start(context.Background(), "polypus.chat")
	leaky := []byte(`https://generativelanguage.googleapis.com/v1?key=super-secret`)
	RecordChatStreamChunk(ctx, 1, leaky, nil, "drop", false)
	span.End()

	for _, sp := range exporter.GetSpans() {
		for _, ev := range sp.Events {
			for _, a := range ev.Attributes {
				if strings.Contains(a.Value.AsString(), "super-secret") {
					t.Fatalf("leaked secret in attribute %s", a.Key)
				}
			}
		}
	}
}

func eventAttrMap(attrs []attribute.KeyValue) map[string]any {
	out := make(map[string]any, len(attrs))
	for _, a := range attrs {
		out[string(a.Key)] = a.Value.AsInterface()
	}
	return out
}
