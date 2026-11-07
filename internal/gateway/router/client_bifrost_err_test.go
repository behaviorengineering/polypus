package router

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestBifrostErrRateLimitFromStatusAndBody(t *testing.T) {
	status := http.StatusTooManyRequests
	berr := &schemas.BifrostError{
		StatusCode: &status,
		Error: &schemas.ErrorField{
			Message: "Capacity temporarily exceeded, please try again.",
		},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RawResponse: json.RawMessage(`{"success":false,"errors":[{"code":3040,"message":"Capacity temporarily exceeded, please try again."}]}`),
		},
	}
	err := bifrostErr(berr, nil, true)
	if !errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
	if derrors.Field(err, ratelimit.FieldCFCode) != ratelimit.WorkersCapacityCode {
		t.Fatalf("cf_code=%q", derrors.Field(err, ratelimit.FieldCFCode))
	}
	if derrors.HTTPStatus(err) != http.StatusTooManyRequests {
		t.Fatalf("status=%d", derrors.HTTPStatus(err))
	}
}

func TestBifrostErrUnavailableOtherwise(t *testing.T) {
	status := http.StatusBadGateway
	err := bifrostErr(&schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "provider call"},
	}, nil, false)
	if !errors.Is(err, derrors.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestBifrostProviderHeaderNilSafe(t *testing.T) {
	if h := bifrostProviderHeader(nil); h != nil {
		t.Fatalf("nil bctx: got %v", h)
	}
	bctx := schemas.NewBifrostContext(t.Context(), schemas.NoDeadline)
	if h := bifrostProviderHeader(bctx); h != nil {
		t.Fatalf("missing key: got %v", h)
	}
}

func TestBifrostErrProductionShaped429(t *testing.T) {
	workersJSON := `{"success":false,"errors":[{"code":3040,"message":"Capacity temporarily exceeded, please try again."}]}`
	status := http.StatusTooManyRequests
	hdr := http.Header{}
	hdr.Set("Retry-After", "12")
	hdr.Set("Cf-Ray", "abc-SYD")

	t.Run("RawMessage", func(t *testing.T) {
		assertProduction429(t, &schemas.BifrostError{
			StatusCode: &status,
			Error:      &schemas.ErrorField{Message: "provider API error (status 429)"},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: json.RawMessage(workersJSON),
			},
		}, hdr)
	})

	t.Run("Map", func(t *testing.T) {
		var raw map[string]any
		if err := json.Unmarshal([]byte(workersJSON), &raw); err != nil {
			t.Fatal(err)
		}
		assertProduction429(t, &schemas.BifrostError{
			StatusCode: &status,
			Error:      &schemas.ErrorField{Message: "provider API error (status 429)"},
			ExtraFields: schemas.BifrostErrorExtraFields{
				RawResponse: raw,
			},
		}, hdr)
	})
}

func assertProduction429(t *testing.T, berr *schemas.BifrostError, hdr http.Header) {
	t.Helper()
	err := bifrostErr(berr, hdr, true)
	if !errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
	if derrors.Field(err, ratelimit.FieldCFCode) != ratelimit.WorkersCapacityCode {
		t.Fatalf("cf_code=%q", derrors.Field(err, ratelimit.FieldCFCode))
	}
	if derrors.Field(err, ratelimit.FieldRetryAfter) != "12" {
		t.Fatalf("retry_after=%q", derrors.Field(err, ratelimit.FieldRetryAfter))
	}
	if derrors.Field(err, ratelimit.FieldCFRay) != "abc-SYD" {
		t.Fatalf("cf_ray=%q", derrors.Field(err, ratelimit.FieldCFRay))
	}
	msg := ""
	for e := err; e != nil; {
		var de *derrors.Error
		if !errors.As(e, &de) || de == nil {
			break
		}
		if de.Code() == derrors.CodeRateLimited {
			if m := strings.TrimSpace(de.Message()); m != "" {
				msg = m
			}
		}
		e = de.Unwrap()
	}
	if !strings.Contains(msg, "Capacity temporarily exceeded") {
		t.Fatalf("message=%q", msg)
	}
	if msg == "provider rate limited" {
		t.Fatal("want Workers message, not router wrapper only")
	}
}

func TestBifrostErrRateLimitNamesTheProvider(t *testing.T) {
	status := http.StatusTooManyRequests
	berr := &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "slow down"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RoutingInfo: schemas.RoutingInfo{Provider: "openrouter"},
		},
	}
	err := bifrostErr(berr, http.Header{"Cf-Ray": {"abc"}}, false)
	if derrors.Field(err, "provider") != "openrouter" {
		t.Fatalf("an OpenRouter 429 must name OpenRouter, not Cloudflare: %v", err)
	}
	if derrors.Field(err, ratelimit.FieldKind) != "" || derrors.Field(err, ratelimit.FieldCFRay) != "" {
		t.Fatalf("a non-Cloudflare 429 must carry no Cloudflare markers: %v", err)
	}
}

func TestBifrostErrIgnoresCloudflareCodesForOtherBackends(t *testing.T) {
	status := http.StatusBadGateway
	berr := &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "upstream failed"},
		ExtraFields: schemas.BifrostErrorExtraFields{
			RawResponse: json.RawMessage(`{"success":false,"errors":[{"code":3040,"message":"busy"}]}`),
		},
	}
	if err := bifrostErr(berr, nil, false); errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("a 502 from a non-Cloudflare backend is not a rate limit: %v", err)
	}
	if err := bifrostErr(berr, nil, true); !errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("Cloudflare's structured 3040 is a capacity limit: %v", err)
	}
}

func TestBifrostErrCloudflareFreeTextCodeOnA5xxIsNotARateLimit(t *testing.T) {
	status := http.StatusBadGateway
	berr := &schemas.BifrostError{
		StatusCode: &status,
		Error:      &schemas.ErrorField{Message: "upstream said 3040 things"},
	}
	err := bifrostErr(berr, nil, true)
	if errors.Is(err, derrors.ErrRateLimited) || derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("a 5xx that only mentions 3040 must stay an upstream failure: %v", err)
	}
}

func TestBifrostErrProviderRefusalKeepsAClientCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		msg    string
		want   derrors.Code
		http   int
	}{
		// The work deploy's smoke on 2026-11-07: OpenRouter's spent weekly budget.
		{"openrouter budget", http.StatusForbidden, "Budget limit exceeded (weekly limit). Contact your org admin. $10.06 of $10.00 used; resets 2026-11-09T00:00:00.000Z.", derrors.CodeNotReady, http.StatusServiceUnavailable},
		{"payment required", http.StatusPaymentRequired, "add credits", derrors.CodeNotReady, http.StatusServiceUnavailable},
		{"credit balance", http.StatusBadRequest, "Your credit balance is too low", derrors.CodeNotReady, http.StatusServiceUnavailable},
		{"forbidden", http.StatusForbidden, "model not allowed for this key", derrors.CodeUnauthorized, http.StatusUnauthorized},
		{"unauthorised", http.StatusUnauthorized, "invalid api key", derrors.CodeUnauthorized, http.StatusUnauthorized},
		{"not found", http.StatusNotFound, "no such model", derrors.CodeNotFound, http.StatusNotFound},
		{"bad request", http.StatusBadRequest, "messages must not be empty", derrors.CodeInvalid, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.status
			berr := &schemas.BifrostError{
				StatusCode:  &status,
				Error:       &schemas.ErrorField{Message: tc.msg},
				ExtraFields: schemas.BifrostErrorExtraFields{RoutingInfo: schemas.RoutingInfo{Provider: "openrouter"}},
			}
			err := bifrostErr(berr, nil, false)
			if got := derrors.CodeOf(err); got != tc.want {
				t.Fatalf("code = %s, want %s (err %v)", got, tc.want, err)
			}
			if got := derrors.HTTPStatus(err); got != tc.http {
				t.Fatalf("HTTP status = %d, want %d", got, tc.http)
			}
			if derrors.Field(err, ratelimit.FieldStatus) != strconv.Itoa(tc.status) {
				t.Fatalf("status field = %q, want %d", derrors.Field(err, ratelimit.FieldStatus), tc.status)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err %v lost the provider message", err)
			}
		})
	}
}

func TestBifrostErrRefusalKeepsAProviderCode(t *testing.T) {
	status := http.StatusBadRequest
	err := bifrostErr(&schemas.BifrostError{
		StatusCode: &status,
		Error: &schemas.ErrorField{
			Message: "plugin refused",
			Error:   derrors.New(derrors.CodeConflict, "plugin", "already running"),
		},
	}, nil, false)
	if got := derrors.CodeOf(err); got != derrors.CodeConflict {
		t.Fatalf("code = %s, want the provider's conflict", got)
	}
}
