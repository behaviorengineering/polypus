package health

import (
	"context"
	"net/http"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// keylessProbePath is an admin route a gateway with no access keys serves to
// anyone, and a locked gateway refuses without a key.
const keylessProbePath = "/v1/admin/models/allow"

// Keyless reports whether the gateway at baseURL runs without access keys: it
// answers a GET of its admin allow list, sent with no key, with 200. ctx must
// carry a deadline. The GET is sent once.
func Keyless(ctx context.Context, client *http.Client, baseURL string) (bool, error) {
	const op = "health.Keyless"
	if ctx == nil {
		return false, derrors.New(derrors.CodeInvalid, op, "context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return false, derrors.New(derrors.CodeFailedPrecondition, op, "context needs a deadline")
	}
	if client == nil {
		return false, derrors.New(derrors.CodeInvalid, op, "http client required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+keylessProbePath, nil)
	if err != nil {
		return false, derrors.Wrap(err, derrors.CodeInvalid, op, "GET "+keylessProbePath)
	}
	resp, err := outbound.DoOnce(ctx, outbound.DepHealth, client, req)
	if err != nil {
		return false, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "GET "+keylessProbePath)
	}
	status := resp.StatusCode
	discardAndClose(resp.Body)
	return status == http.StatusOK, nil
}
