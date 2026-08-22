package observability

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRedactURLString_geminiQueryKey(t *testing.T) {
	raw := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=super-secret&alt=sse"
	got := redactURLString(raw)
	if strings.Contains(got, "super-secret") {
		t.Fatalf("leaked secret in %s", got)
	}
	if !strings.Contains(got, "key=REDACTED") {
		t.Fatalf("missing REDACTED in %s", got)
	}
}

func TestRedactHTTPError_urlError(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "https://example.com/v1?key=super-secret",
		Err: errors.New("timeout"),
	}
	got := redactHTTPError(err).Error()
	if strings.Contains(got, "super-secret") {
		t.Fatalf("leaked secret in %s", got)
	}
}
