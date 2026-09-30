package observability

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestRecordDownstreamModelSetsAttribute(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	ctx, span := tp.Tracer("test").Start(context.Background(), "polypus.router")
	RecordDownstreamModel(ctx, "cf_local/@cf/a")
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans: %d", len(spans))
	}
	var found bool
	for _, kv := range spans[0].Attributes {
		if kv.Key == "polypus.downstream_model" && kv.Value.AsString() == "cf_local/@cf/a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("attrs: %+v", attributeSet(spans[0].Attributes))
	}
}

func attributeSet(attrs []attribute.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		out[string(kv.Key)] = kv.Value.AsString()
	}
	return out
}
