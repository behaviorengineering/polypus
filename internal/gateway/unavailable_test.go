package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/sony/gobreaker"
)

func TestWriteUnavailableErrorBreakerOpen(t *testing.T) {
	err := fmt.Errorf("upstream cf_local unavailable: %w", gobreaker.ErrOpenState)
	rec := httptest.NewRecorder()
	writeUnavailableError(rec, err, err.Error(), "cf_local")
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
	err := fmt.Errorf("upstream leaf unavailable: %w", gobreaker.ErrTooManyRequests)
	rec := httptest.NewRecorder()
	writeUnavailableError(rec, err, err.Error(), "leaf")
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Polypus.CircuitBreaker.State != "half-open" {
		t.Fatalf("state=%q", body.Polypus.CircuitBreaker.State)
	}
}

func TestWriteUpstreamDialErrorSwitchyardUnreachableNoRetryAfter(t *testing.T) {
	err := errors.New("dial tcp 127.0.0.1:4000: connect: connection refused")
	rec := httptest.NewRecorder()
	writeUpstreamDialError(rec, err, "polypus: switchyard unavailable: ", upstream.NameSwitchyard, isSwitchyardUnreachable)
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
			With(cloudflare.FieldCFCode, cloudflare.WorkersQuotaCode),
		derrors.CodeRateLimited,
		"router.bifrost",
		"provider rate limited",
	)
	rec := httptest.NewRecorder()
	writeUpstreamDialError(rec, err, "", "cf_local", nil)
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
	writeUpstreamDialError(rec, err, "", "cf_local", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body=%q", rec.Body.String())
	}
}

func TestWriteHandlerErrorBreakerResolvesUpstream(t *testing.T) {
	err := fmt.Errorf("upstream cf_local unavailable: %w", gobreaker.ErrOpenState)
	rec := httptest.NewRecorder()
	writeHandlerError(rec, err)
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
