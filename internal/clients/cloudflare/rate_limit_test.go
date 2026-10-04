package cloudflare

import (
	"errors"
	"net/http"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestClassifyRateLimitQuota3036(t *testing.T) {
	body := []byte(`{"success":false,"errors":[{"code":3036,"message":"You have used up your daily free allocation of 10,000 neurons."}]}`)
	header := http.Header{}
	header.Set("Retry-After", "3600")
	header.Set("Cf-Ray", "abc123")
	err := ClassifyRateLimit("cloudflare.Synthesize", http.StatusTooManyRequests, header, body)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if !errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("code=%s", derrors.CodeOf(err))
	}
	if derrors.HTTPStatus(err) != http.StatusTooManyRequests {
		t.Fatalf("status=%d", derrors.HTTPStatus(err))
	}
	if got := err.Fields()[FieldCFCode]; got != WorkersQuotaCode {
		t.Fatalf("cf_code=%q", got)
	}
	if got := err.Fields()[FieldKind]; got != KindQuota {
		t.Fatalf("kind=%q", got)
	}
	if got := err.Fields()[FieldRetryAfter]; got != "3600" {
		t.Fatalf("retry_after=%q", got)
	}
	if got := err.Fields()[FieldCFRay]; got != "abc123" {
		t.Fatalf("cf_ray=%q", got)
	}
}

func TestClassifyRateLimitCapacity3040(t *testing.T) {
	body := []byte(`{"success":false,"errors":[{"code":"3040","message":"Capacity temporarily exceeded, please try again."}]}`)
	err := ClassifyRateLimit("cloudflare.workers", 0, nil, body)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if got := err.Fields()[FieldCFCode]; got != WorkersCapacityCode {
		t.Fatalf("cf_code=%q", got)
	}
	if got := err.Fields()[FieldKind]; got != KindCapacity {
		t.Fatalf("kind=%q", got)
	}
}

func TestClassifyRateLimitEdgeHint(t *testing.T) {
	err := ClassifyRateLimit("cloudflare.workers", http.StatusTooManyRequests, nil, []byte("error code: 1015"))
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if got := err.Fields()[FieldCFCode]; got != EdgeBlockCode {
		t.Fatalf("cf_code=%q", got)
	}
}

func TestClassifyRateLimitGeneric429(t *testing.T) {
	err := ClassifyRateLimit("cloudflare.workers", http.StatusTooManyRequests, nil, []byte(`{"error":"slow down"}`))
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if got := err.Fields()[FieldKind]; got != KindRequest {
		t.Fatalf("kind=%q", got)
	}
	if _, ok := err.Fields()[FieldCFCode]; ok {
		t.Fatalf("unexpected cf_code")
	}
}

func TestClassifyRateLimitNotThrottle(t *testing.T) {
	err := ClassifyRateLimit("cloudflare.workers", http.StatusBadGateway, nil, []byte(`{"success":false,"errors":[{"code":10000,"message":"auth"}]}`))
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
}
