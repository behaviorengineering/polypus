package observability

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
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

func TestRedactURLString_userinfoWithoutSecretQuery(t *testing.T) {
	for _, raw := range []string{
		"https://user:s3cret@host.example/v1",
		"https://s3cret@host.example/v1?alt=sse",
	} {
		got := redactURLString(raw)
		if strings.Contains(got, "s3cret") {
			t.Fatalf("redactURLString(%q) leaked userinfo: %s", raw, got)
		}
		if !strings.Contains(got, "host.example/v1") {
			t.Fatalf("redactURLString(%q) lost the host or path: %s", raw, got)
		}
	}
	if got := redactURLString("https://host.example/v1?alt=sse"); got != "https://host.example/v1?alt=sse" {
		t.Fatalf("clean URL changed: %s", got)
	}
}

func TestRedactURLsInText_userinfoWithoutSecretQuery(t *testing.T) {
	got := redactURLsInText(`Post "https://user:s3cret@host.example/v1": dial tcp: refused`)
	if strings.Contains(got, "s3cret") {
		t.Fatalf("leaked userinfo: %s", got)
	}
	if !strings.Contains(got, "host.example/v1") {
		t.Fatalf("lost host: %s", got)
	}
}

func TestRedactHTTPError_wrappedURLErrorKeepsChain(t *testing.T) {
	inner := &url.Error{Op: "Post", URL: "https://user:s3cret@host.example/v1", Err: errors.New("timeout")}
	err := derrors.Wrap(fmt.Errorf("hop: %w", inner), derrors.CodeUnavailable, "client.Do", "post")
	got := redactHTTPError(err)
	if strings.Contains(got.Error(), "s3cret") {
		t.Fatalf("leaked userinfo: %s", got)
	}
	if derrors.CodeOf(got) != derrors.CodeUnavailable {
		t.Fatalf("code = %q, want the wrapped domain code", derrors.CodeOf(got))
	}
}
