package router

import (
	"encoding/json"
	"errors"
	"net/http"
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
	err := bifrostErr(berr)
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
	})
	if !errors.Is(err, derrors.ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}
