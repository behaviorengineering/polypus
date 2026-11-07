package ratelimit

import (
	"errors"
	"net/http"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestClassifyQuota3036(t *testing.T) {
	body := []byte(`{"success":false,"errors":[{"code":3036,"message":"You have used up your daily free allocation of 10,000 neurons."}]}`)
	header := http.Header{}
	header.Set("Retry-After", "3600")
	header.Set("Cf-Ray", "abc123")
	err, _ := Classify("cloudflare.Synthesize", http.StatusTooManyRequests, header, body)
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

func TestClassifyCapacity3040(t *testing.T) {
	body := []byte(`{"success":false,"errors":[{"code":"3040","message":"Capacity temporarily exceeded, please try again."}]}`)
	err, _ := Classify("cloudflare.workers", 0, nil, body)
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

func TestClassifyEdgeHint(t *testing.T) {
	err, _ := Classify("cloudflare.workers", http.StatusTooManyRequests, nil, []byte("error code: 1015"))
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if got := err.Fields()[FieldCFCode]; got != EdgeBlockCode {
		t.Fatalf("cf_code=%q", got)
	}
}

func TestClassifyGeneric429(t *testing.T) {
	err, _ := Classify("cloudflare.workers", http.StatusTooManyRequests, nil, []byte(`{"error":"slow down"}`))
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

func TestClassifyXRateLimitHeaders(t *testing.T) {
	header := http.Header{}
	header.Set("X-RateLimit-Limit", "100")
	header.Set("X-RateLimit-Remaining", "0")
	err, _ := Classify("cloudflare.workers", http.StatusTooManyRequests, header, nil)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if got := err.Fields()[FieldHeaderPrefix+"x-ratelimit-limit"]; got != "100" {
		t.Fatalf("limit=%q", got)
	}
	if got := err.Fields()[FieldHeaderPrefix+"x-ratelimit-remaining"]; got != "0" {
		t.Fatalf("remaining=%q", got)
	}
}

func TestClassifyNotThrottle(t *testing.T) {
	err, _ := Classify("cloudflare.workers", http.StatusBadGateway, nil, []byte(`{"success":false,"errors":[{"code":10000,"message":"auth"}]}`))
	if err != nil {
		t.Fatalf("unexpected %v", err)
	}
}

func TestClassifyStatusOnlyReads429AndAddsNoCloudflareMarkers(t *testing.T) {
	if err, _ := ClassifyStatus("gemini", http.StatusBadGateway, nil, []byte(`{"errors":[{"code":3036}]}`)); err != nil {
		t.Fatalf("a non-429 is not a rate limit for a non-Cloudflare backend: %v", err)
	}
	header := http.Header{}
	header.Set("Retry-After", "7")
	header.Set("Cf-Ray", "ray")
	err, _ := ClassifyStatus("gemini", http.StatusTooManyRequests, header, nil, "slow down")
	if err == nil || err.Fields()[FieldRetryAfter] != "7" {
		t.Fatalf("a 429 keeps Retry-After: %v", err)
	}
	if err.Fields()[FieldKind] != "" || err.Fields()[FieldCFRay] != "" {
		t.Fatalf("no Cloudflare markers: %v", err.Fields())
	}
}

func TestClassifyReadsTextCodesOnlyOnA429(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		hints  []string
		want   string // cf_code, or "-" for no rate limit.
	}{
		{"5xx text mentions 3040", http.StatusInternalServerError, `upstream said 3040 things`, nil, "-"},
		{"5xx message mentions 3036", http.StatusBadGateway, `{"errors":[{"code":7000,"message":"see 3036"}]}`, nil, "-"},
		{"4xx hint mentions 1015", http.StatusBadRequest, ``, []string{"error code: 1015"}, "-"},
		{"5xx structured code", http.StatusInternalServerError, `{"errors":[{"code":3040,"message":"busy"}]}`, nil, WorkersCapacityCode},
		{"no status structured string code", 0, `{"errors":[{"code":"3036"}]}`, nil, WorkersQuotaCode},
		{"5xx hint is exactly a code", http.StatusBadGateway, ``, []string{"provider failed", " 3040 "}, WorkersCapacityCode},
		{"429 text code labels the limit", http.StatusTooManyRequests, `error code: 1015`, nil, EdgeBlockCode},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err, _ := Classify("cloudflare.workers", tc.status, nil, []byte(tc.body), tc.hints...)
			if tc.want == "-" {
				if err != nil {
					t.Fatalf("not a rate limit, got %v", err)
				}
				return
			}
			if err == nil || err.Fields()[FieldCFCode] != tc.want {
				t.Fatalf("want cf_code %q, got %v", tc.want, err)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value  string
		want   time.Duration
		wantOK bool
	}{
		{"", 0, false},
		{"120", 2 * time.Minute, true},
		{" 5 ", 5 * time.Second, true},
		{"0", 0, true},
		{"-1", 0, false},
		{"soon", 0, false},
		{"1.5", 0, false},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{now.Add(-time.Hour).Format(http.TimeFormat), 0, true},
		{"99999999999999999", maxRetryAfterSeconds * time.Second, true},
	} {
		h := http.Header{}
		if tc.value != "" {
			h.Set("Retry-After", tc.value)
		}
		got, ok := RetryAfter(h, now)
		if got != tc.want || ok != tc.wantOK {
			t.Fatalf("Retry-After %q: got %s, %v; want %s, %v", tc.value, got, ok, tc.want, tc.wantOK)
		}
	}
	if _, ok := RetryAfter(nil, now); ok {
		t.Fatal("a nil header has no Retry-After")
	}
}
