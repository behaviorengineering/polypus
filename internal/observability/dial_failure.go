package observability

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

// EndDialSpan records err on the span with failure attribution for upstream dials.
func EndDialSpan(span trace.Span, err error, upstreamName string) {
	if span == nil {
		return
	}
	if err == nil {
		span.SetStatus(codes.Ok, "")
		span.End()
		return
	}
	AnnotateDialFailure(span, err, upstreamName)
	span.End()
}

// AnnotateDialFailure sets OpenInference-friendly failure attributes on span.
func AnnotateDialFailure(span trace.Span, err error, upstreamName string) {
	if span == nil || err == nil {
		return
	}
	annotateSpanError(span, err)
	cls := upstream.ClassifyDialFailure(err, upstreamName)
	if cls.Layer == "" {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("polypus.failure.layer", cls.Layer),
		attribute.String("polypus.failure.upstream", cls.Upstream),
	}
	if cls.HTTPStatus > 0 {
		attrs = append(attrs, attribute.Int("polypus.failure.http_status", cls.HTTPStatus))
	}
	if cls.BreakerState != "" {
		attrs = append(attrs, attribute.String("polypus.circuit_breaker.state", cls.BreakerState))
		span.AddEvent("circuit_breaker", trace.WithAttributes(
			attribute.String("upstream", cls.Upstream),
			attribute.String("state", cls.BreakerState),
		))
	}
	span.SetAttributes(attrs...)
}
