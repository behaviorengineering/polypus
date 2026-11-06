package observability

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// DialFailure labels a failed upstream dial on its span. The gateway decides the
// labels (it knows its breakers and upstreams); observability only records them.
// A zero Layer records the error without failure attribution.
type DialFailure struct {
	// Layer is the hop that failed, such as "leaf", "cloudflare" or "polypus_breaker".
	Layer string
	// Upstream names the backend or dependency the dial targeted.
	Upstream string
	// BreakerState is the circuit breaker state when the breaker refused the call.
	BreakerState string
	// HTTPStatus is the upstream status the failure recorded; 0 means none.
	HTTPStatus int
}

// EndDialSpan records err on the span with the failure labels f, then ends it.
// On success f is ignored.
func EndDialSpan(span trace.Span, err error, f DialFailure) {
	if span == nil {
		return
	}
	if err == nil {
		span.SetStatus(codes.Ok, "")
		span.End()
		return
	}
	annotateDialFailure(span, err, f)
	span.End()
}

// annotateDialFailure sets OpenInference-friendly failure attributes on span.
func annotateDialFailure(span trace.Span, err error, f DialFailure) {
	if span == nil || err == nil {
		return
	}
	annotateSpanError(span, err)
	if f.Layer == "" {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("polypus.failure.layer", f.Layer),
		attribute.String("polypus.failure.upstream", f.Upstream),
	}
	if f.HTTPStatus > 0 {
		attrs = append(attrs, attribute.Int("polypus.failure.http_status", f.HTTPStatus))
	}
	if f.BreakerState != "" {
		attrs = append(attrs, attribute.String("polypus.circuit_breaker.state", f.BreakerState))
		span.AddEvent("circuit_breaker", trace.WithAttributes(
			attribute.String("upstream", f.Upstream),
			attribute.String("state", f.BreakerState),
		))
	}
	span.SetAttributes(attrs...)
}
