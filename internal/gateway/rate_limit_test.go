package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

func TestWriteRateLimitErrorJSON(t *testing.T) {
	err := derrors.Wrap(
		derrors.New(derrors.CodeRateLimited, "cloudflare.workers", "You have used up your daily free allocation of 10,000 neurons. Quota resets daily at 00:00 UTC.").
			With(ratelimit.FieldCFCode, ratelimit.WorkersQuotaCode).
			With(ratelimit.FieldRetryAfter, "120").
			With(ratelimit.FieldCFRay, "ray-1"),
		derrors.CodeRateLimited,
		"router.bifrost",
		"provider rate limited",
	)
	rec := httptest.NewRecorder()
	if !testShared.writeRateLimitError(rec, err) {
		t.Fatal("expected write")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "120" {
		t.Fatalf("Retry-After=%q", rec.Header().Get("Retry-After"))
	}
	if rec.Header().Get("Cf-Ray") != "ray-1" {
		t.Fatalf("Cf-Ray=%q", rec.Header().Get("Cf-Ray"))
	}
	var body openaiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Type != "rate_limit_error" || body.Error.Code != ratelimit.WorkersQuotaCode {
		t.Fatalf("error=%+v", body.Error)
	}
	if body.Error.Message == "provider rate limited" {
		t.Fatal("want Cloudflare quota message, not router wrapper")
	}
}

func TestWriteRateLimitErrorXRateLimitHeaders(t *testing.T) {
	err := derrors.Wrap(
		derrors.New(derrors.CodeRateLimited, "cloudflare.workers", "capacity").
			With(ratelimit.FieldCFCode, ratelimit.WorkersCapacityCode).
			With(ratelimit.FieldRetryAfter, "30").
			With(ratelimit.FieldCFRay, "ray-syd").
			With(ratelimit.FieldHeaderPrefix+"x-ratelimit-limit", "100").
			With(ratelimit.FieldHeaderPrefix+"x-ratelimit-remaining", "0"),
		derrors.CodeRateLimited,
		"router.bifrost",
		"provider rate limited",
	)
	rec := httptest.NewRecorder()
	if !testShared.writeRateLimitError(rec, err) {
		t.Fatal("expected write")
	}
	if rec.Header().Get("X-RateLimit-Limit") != "100" {
		t.Fatalf("X-RateLimit-Limit=%q", rec.Header().Get("X-RateLimit-Limit"))
	}
	if rec.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("X-RateLimit-Remaining=%q", rec.Header().Get("X-RateLimit-Remaining"))
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

func TestOpenAIRateLimitBodyFromWorkersJSON(t *testing.T) {
	raw := []byte(`{"success":false,"errors":[{"code":3040,"message":"Capacity temporarily exceeded, please try again."}]}`)
	out, ok := openaiRateLimitBody(http.StatusTooManyRequests, nil, raw)
	if !ok {
		t.Fatal("expected rewrite")
	}
	var body openaiErrorBody
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != ratelimit.WorkersCapacityCode {
		t.Fatalf("code=%q", body.Error.Code)
	}
	if body.Error.Type != "rate_limit_error" {
		t.Fatalf("type=%q", body.Error.Type)
	}
}
