package observability

import (
	"net/http"
	"net/url"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/redact"
)

const redactedSecret = redact.Secret

// redactURLString masks userinfo credentials and secret query values in raw. A URL
// that needs neither comes back unchanged.
func redactURLString(raw string) string { return redact.URL(raw) }

func redactQueryString(raw string) string { return redact.Query(raw) }

func redactURLsInText(s string) string { return redact.Text(s) }

func redactHTTPError(err error) error {
	if err == nil {
		return nil
	}
	// Only an outermost *url.Error is copied with its URL redacted; errors.As would
	// also match one deeper in the chain and drop the layers above it.
	if urlErr, ok := err.(*url.Error); ok { //nolint:errorlint // the outermost error only, by design
		out := *urlErr
		out.URL = redactURLString(urlErr.URL)
		return &out
	}
	// A url.Error deeper in the chain: keep the chain (and its domain code and op),
	// redact only the text.
	msg := err.Error()
	redacted := redactURLsInText(msg)
	if redacted == msg {
		return err
	}
	return &redactedError{orig: err, msg: redacted}
}

type redactedError struct {
	orig error
	msg  string
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.orig }

func redactSpanRequestURL(span trace.Span, req *http.Request) {
	if span == nil || !span.IsRecording() || req == nil || req.URL == nil {
		return
	}
	span.SetAttributes(
		attribute.String("url.full", redactURLString(req.URL.String())),
		attribute.String("url.path", sanitizeAttrValue("url.path", req.URL.Path)),
	)
	if req.URL.RawQuery != "" {
		span.SetAttributes(attribute.String("url.query", redactQueryString(req.URL.RawQuery)))
	}
}

func sanitizeAttrValue(key, value string) string {
	switch key {
	case "url.query":
		return redactQueryString(value)
	case "url.full", "http.url":
		return redactURLString(value)
	default:
		return redactURLsInText(value)
	}
}

// RedactDumpText applies the failure-dump redaction (URL credentials and keys) to a
// dump body written outside olly, such as a dropped stream chunk.
func RedactDumpText(s string) string {
	return redactURLsInText(s)
}
