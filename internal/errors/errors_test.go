package errors

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWrapChainIsAndFields(t *testing.T) {
	leaf := fmt.Errorf("post /run: %w", context.DeadlineExceeded)
	cf := Wrap(leaf, CodeTimeout, "cloudflare.Synthesize", "post /run").
		With("backend", "cf_local").
		With("model", "@cf/deepgram/aura-2-en")
	plugin := Wrap(cf, CodeUnavailable, "cloudflare.runSpeech", "synthesize")
	router := Wrap(plugin, CodeUnavailable, "router.Synthesize", "bifrost speech")

	if !errors.Is(router, ErrUnavailable) {
		t.Fatal("want errors.Is router, ErrUnavailable")
	}
	if !errors.Is(router, context.DeadlineExceeded) {
		t.Fatal("want errors.Is router, context.DeadlineExceeded")
	}
	if CodeOf(router) != CodeUnavailable {
		t.Fatalf("outer code=%s", CodeOf(router))
	}
	if got := cf.Fields()["backend"]; got != "cf_local" {
		t.Fatalf("fields=%v", cf.Fields())
	}
	if _, ok := router.Fields()["backend"]; ok {
		t.Fatal("router must not inherit cloudflare fields; each hop owns its own")
	}
	if !strings.Contains(router.Error(), "router.Synthesize") {
		t.Fatalf("Error()=%q", router.Error())
	}
	if !strings.Contains(router.Error(), "cloudflare.Synthesize") {
		t.Fatalf("Error() missing inner op: %q", router.Error())
	}

	detail := fmt.Sprintf("%+v", router)
	if !strings.Contains(detail, "code=unavailable") || !strings.Contains(detail, "caused by:") {
		t.Fatalf("%+v=%s", router, detail)
	}
}

func TestNewDomainErrorAndSentinel(t *testing.T) {
	err := NewDomainError(CodeNotFound, "backend missing", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatal("want Is ErrNotFound")
	}
	if err.Op() != "" {
		t.Fatalf("op=%q", err.Op())
	}
}

func TestWrapNilCause(t *testing.T) {
	err := Wrap(nil, CodeInvalid, "router.Synthesize", "input required")
	if err.Unwrap() != nil {
		t.Fatal("nil cause must not unwrap")
	}
	if HTTPStatus(err) != http.StatusBadRequest {
		t.Fatalf("status=%d", HTTPStatus(err))
	}
}

func TestWithDoesNotMutate(t *testing.T) {
	base := New(CodeInvalid, "op", "msg")
	next := base.With("k", "v")
	if base.Fields() != nil {
		t.Fatalf("base mutated: %v", base.Fields())
	}
	if next.Fields()["k"] != "v" {
		t.Fatalf("next=%v", next.Fields())
	}
}

func TestHTTPStatusTimeout(t *testing.T) {
	if HTTPStatus(ErrTimeout) != http.StatusGatewayTimeout {
		t.Fatalf("timeout status=%d", HTTPStatus(ErrTimeout))
	}
	if HTTPStatus(ErrNotReady) != http.StatusServiceUnavailable {
		t.Fatalf("not_ready status=%d", HTTPStatus(ErrNotReady))
	}
	if HTTPStatus(fmt.Errorf("plain")) != http.StatusInternalServerError {
		t.Fatalf("plain=%d", HTTPStatus(fmt.Errorf("x")))
	}
}
