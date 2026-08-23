package observability

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type errorDetailTransport struct {
	base http.RoundTripper
}

func (t *errorDetailTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	span := trace.SpanFromContext(req.Context())
	redactSpanRequestURL(span, req)
	started := time.Now()
	resp, err := t.base.RoundTrip(req)
	if !span.IsRecording() {
		return resp, redactHTTPError(err)
	}
	redactSpanRequestURL(span, req)
	span.SetAttributes(attribute.Int64("http.duration_ms", time.Since(started).Milliseconds()))
	if err != nil {
		redacted := redactHTTPError(err)
		span.SetAttributes(httpErrorAttributes(redacted)...)
		return resp, redacted
	}
	if resp != nil && resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, resp.Status)
		span.SetAttributes(attribute.Bool("http.error", true))
	}
	return resp, err
}

func annotateSpanError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	redacted := redactHTTPError(err)
	span.RecordError(redacted)
	span.SetStatus(codes.Error, redacted.Error())
	span.SetAttributes(httpErrorAttributes(redacted)...)
}

func httpErrorAttributes(err error) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("error.class", classifyHTTPError(err)),
		attribute.String("error.message", redactURLsInText(err.Error())),
		attribute.Bool("timeout", isTimeout(err)),
		attribute.Bool("context.deadline_exceeded", errors.Is(err, context.DeadlineExceeded)),
		attribute.Bool("context.canceled", errors.Is(err, context.Canceled)),
	}
}

func classifyHTTPError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if isTimeout(err) {
		return "timeout"
	}
	return "http_error"
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isTimeout(urlErr.Err)
	}
	return false
}

func withErrorDetail(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*errorDetailTransport); ok {
		return base
	}
	return &errorDetailTransport{base: base}
}

// RecordProxyIO annotates the current span with backend URL, streaming, status, and body size.
func RecordProxyIO(ctx context.Context, target string, streaming bool, statusCode, bodyBytes int) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("url.full", redactURLString(target)),
		attribute.Bool("llm.streaming", streaming),
	}
	if statusCode > 0 {
		attrs = append(attrs, attribute.Int("http.response.status_code", statusCode))
	}
	if bodyBytes >= 0 {
		attrs = append(attrs, attribute.Int("http.response.body.size", bodyBytes))
	}
	span.SetAttributes(attrs...)
}
