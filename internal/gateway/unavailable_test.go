package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
	"github.com/sony/gobreaker"
)

func TestWriteUnavailableErrorBreakerOpen(t *testing.T) {
	err := upstream.UnavailableError("cf_local", gobreaker.ErrOpenState)
	rec := httptest.NewRecorder()
	testShared.writeUnavailableError(rec, err, err.Error(), "cf_local")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After=%q", got)
	}
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Type != "unavailable_error" || body.Error.Code != upstream.LayerPolypusBreaker {
		t.Fatalf("error=%+v", body.Error)
	}
	if body.Polypus == nil || body.Polypus.Failure.Layer != upstream.LayerPolypusBreaker {
		t.Fatalf("polypus=%+v", body.Polypus)
	}
	if body.Polypus.CircuitBreaker == nil || body.Polypus.CircuitBreaker.State != "open" {
		t.Fatalf("breaker=%+v", body.Polypus.CircuitBreaker)
	}
}

func TestWriteUnavailableErrorHalfOpen(t *testing.T) {
	err := upstream.UnavailableError("leaf", gobreaker.ErrTooManyRequests)
	rec := httptest.NewRecorder()
	testShared.writeUnavailableError(rec, err, err.Error(), "leaf")
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Polypus.CircuitBreaker.State != "half-open" {
		t.Fatalf("state=%q", body.Polypus.CircuitBreaker.State)
	}
}

// refusedDial returns the error of a dial to a port nothing listens on.
func refusedDial(t *testing.T) error {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // a closed port refuses the next dial.
	_, dialErr := net.Dial("tcp", addr)
	if dialErr == nil {
		t.Fatal("expected the dial to be refused")
	}
	return dialErr
}

func TestWriteUpstreamDialErrorSwitchyardUnreachableNoRetryAfter(t *testing.T) {
	err := refusedDial(t)
	rec := httptest.NewRecorder()
	testShared.writeUpstreamDialError(rec, err, "polypus: switchyard unavailable: ", upstream.NameSwitchyard, isSwitchyardUnreachable)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "" {
		t.Fatalf("Retry-After=%q want empty", ra)
	}
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != upstream.LayerSwitchyard {
		t.Fatalf("code=%q", body.Error.Code)
	}
	if body.Polypus.CircuitBreaker != nil {
		t.Fatalf("unexpected breaker meta %+v", body.Polypus.CircuitBreaker)
	}
}

func TestWriteUpstreamDialErrorRateLimitPrecedence(t *testing.T) {
	err := derrors.Wrap(
		derrors.New(derrors.CodeRateLimited, "cloudflare.workers", "quota").
			With(ratelimit.FieldCFCode, ratelimit.WorkersQuotaCode),
		derrors.CodeRateLimited,
		"router.bifrost",
		"provider rate limited",
	)
	rec := httptest.NewRecorder()
	testShared.writeUpstreamDialError(rec, err, "", "cf_local", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Polypus != nil {
		t.Fatalf("polypus=%+v", body.Polypus)
	}
	if body.Error.Type != "rate_limit_error" {
		t.Fatalf("type=%q", body.Error.Type)
	}
}

func TestWriteUpstreamDialErrorResponseWrittenNoOp(t *testing.T) {
	err := fmt.Errorf("stream: %w", upstream.ErrResponseWritten)
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusOK)
	testShared.writeUpstreamDialError(rec, err, "", "cf_local", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

func TestWriteHandlerErrorBreakerResolvesUpstream(t *testing.T) {
	err := upstream.UnavailableError("cf_local", gobreaker.ErrOpenState)
	rec := httptest.NewRecorder()
	testShared.writeHandlerError(rec, err)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Polypus == nil || body.Polypus.Failure.Upstream != "cf_local" {
		t.Fatalf("polypus=%+v", body.Polypus)
	}
}

func TestSwitchyardUnreachableMatchesRealDialFailures(t *testing.T) {
	dialErr := refusedDial(t)
	if !isSwitchyardUnreachable(fmt.Errorf("wrapped: %w", dialErr)) {
		t.Fatalf("a refused dial is unreachable: %v", dialErr)
	}
	if isSwitchyardUnreachable(errors.New("connection refused, said a body")) {
		t.Fatal("text alone is not a dial failure")
	}
}
