package observability

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const redactedSecret = "REDACTED"

var embeddedHTTPURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

func isSecretQueryName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "key", "api_key", "apikey", "api-key", "access_token", "token",
		"secret", "password", "auth", "authorization", "client_secret":
		return true
	}
	return strings.Contains(n, "api_key") ||
		strings.HasSuffix(n, "_secret") ||
		strings.HasSuffix(n, "_token") ||
		strings.HasSuffix(n, "_key")
}

func redactQueryValues(q url.Values) bool {
	changed := false
	for name := range q {
		if !isSecretQueryName(name) {
			continue
		}
		q.Set(name, redactedSecret)
		changed = true
	}
	return changed
}

func redactURLString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.RawQuery == "" {
		return raw
	}
	q := u.Query()
	if !redactQueryValues(q) {
		return raw
	}
	u.RawQuery = q.Encode()
	u.User = nil
	return u.String()
}

func redactQueryString(raw string) string {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if !redactQueryValues(q) {
		return raw
	}
	return q.Encode()
}

func redactURLsInText(s string) string {
	if s == "" || !strings.Contains(s, "://") {
		return s
	}
	return embeddedHTTPURL.ReplaceAllStringFunc(s, redactURLString)
}

func redactHTTPError(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		out := *urlErr
		out.URL = redactURLString(urlErr.URL)
		return &out
	}
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
