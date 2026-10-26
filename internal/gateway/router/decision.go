package router

import (
	"context"
	"encoding/json"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/providers/typesafe"
	"github.com/maximhq/bifrost/core/schemas"
)

// SystemOneDecisionRaw sends a TypeSafe-shaped SystemOne body via Bifrost Decision.
// The backend must be registered as native OpenRouter (CustomProviderConfig nil).
func (c *Client) SystemOneDecisionRaw(ctx context.Context, call Call) ([]byte, error) {
	backendID, downstream, body, timeout := call.BackendID, call.Model, call.Body, call.Timeout
	if c == nil || c.bf == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "router.SystemOneDecisionRaw", "bifrost not configured")
	}
	var native typesafe.TypesafeDecisionRequest
	if err := json.Unmarshal(body, &native); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.SystemOneDecisionRaw", "parse systemone body")
	}
	if downstream != "" {
		native.Model = downstream
	}
	bctx := c.bifrostContext(ctx, timeout, false)
	bfReq, err := native.ToBifrostDecisionRequest(bctx)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "router.SystemOneDecisionRaw", "convert decision request")
	}
	bfReq.Provider = schemas.ModelProvider(backendID)
	if downstream != "" {
		bfReq.Model = downstream
	}
	resp, berr := c.bf.DecisionRequest(bctx, bfReq)
	if berr != nil {
		return nil, c.mapBifrostErr(backendID, berr, bifrostProviderHeader(bctx))
	}
	if resp == nil {
		return nil, derrors.New(derrors.CodeUnavailable, "router.SystemOneDecisionRaw", "empty decision response from backend").
			With("backend", backendID).
			With("model", downstream)
	}
	if len(resp.NativeResponse) > 0 {
		return resp.NativeResponse, nil
	}
	raw, err := rawProviderJSON(resp.ExtraFields.RawResponse)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 {
		return raw, nil
	}
	return nil, derrors.New(derrors.CodeUnavailable, "router.SystemOneDecisionRaw", "decision response missing provider body").
		With("backend", backendID).
		With("model", downstream)
}
