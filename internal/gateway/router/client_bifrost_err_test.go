package router

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
	err := bifrostErr(berr, nil)
	if !errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
	if derrors.Field(err, cloudflare.FieldCFCode) != cloudflare.WorkersCapacityCode {
		t.Fatalf("cf_code=%q", derrors.Field(err, cloudflare.FieldCFCode))
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
	}, nil)
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
	err := bifrostErr(berr, hdr)
	if !errors.Is(err, derrors.ErrRateLimited) {
		t.Fatalf("got %v", err)
	}
	if derrors.Field(err, cloudflare.FieldCFCode) != cloudflare.WorkersCapacityCode {
		t.Fatalf("cf_code=%q", derrors.Field(err, cloudflare.FieldCFCode))
	}
	if derrors.Field(err, cloudflare.FieldRetryAfter) != "12" {
		t.Fatalf("retry_after=%q", derrors.Field(err, cloudflare.FieldRetryAfter))
	}
	if derrors.Field(err, cloudflare.FieldCFRay) != "abc-SYD" {
		t.Fatalf("cf_ray=%q", derrors.Field(err, cloudflare.FieldCFRay))
	}
	msg := ""
	for e := err; e != nil; {
		if de, ok := e.(*derrors.Error); ok && de != nil && de.Code() == derrors.CodeRateLimited {
			if m := strings.TrimSpace(de.Message()); m != "" {
				msg = m
			}
			e = de.Unwrap()
			continue
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	if !strings.Contains(msg, "Capacity temporarily exceeded") {
		t.Fatalf("message=%q", msg)
	}
	if msg == "provider rate limited" {
		t.Fatal("want Workers message, not router wrapper only")
	}
}
