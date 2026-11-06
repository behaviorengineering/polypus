package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

// runMaxBody caps a Workers AI /run reply (audio, transcripts, SystemOne, batch
// results; a batch input may itself be 10 MB).
const runMaxBody = 64 << 20

// errorBodyMax caps the upstream body kept on an error.
const errorBodyMax = 256

// workersAIError is one entry of a Workers AI errors array.
type workersAIError struct {
	Message string `json:"message"`
}

// firstErrorMessage returns errs[0].message, or fallback when there is none.
func firstErrorMessage(errs []workersAIError, fallback string) string {
	if len(errs) > 0 {
		if msg := strings.TrimSpace(errs[0].Message); msg != "" {
			return msg
		}
	}
	return fallback
}

// workersAIErrorMessage reads errors[0].message from a Workers AI body, or returns
// fallback.
func workersAIErrorMessage(raw []byte, fallback string) string {
	var envelope struct {
		Errors []workersAIError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fallback
	}
	return firstErrorMessage(envelope.Errors, fallback)
}

// runOnce sends a Workers AI /run request once (no retry: these calls are paid or not
// idempotent). ctx must carry the caller's deadline; the outbound seam fails closed
// without one, and its code (for example CodeFailedPrecondition) is kept.
func (c *Client) runOnce(ctx context.Context, op string, req *http.Request) (*http.Response, error) {
	if c == nil || c.httpClient == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, op, "not configured")
	}
	resp, err := c.policies.DoOnce(ctx, c.dep(), c.httpClient, req)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "post /run").
			With("target", req.URL.String())
	}
	return resp, nil
}

// upstreamError maps a non-2xx /run answer: a throttle becomes a rate-limit error, any
// other status its closest domain code (a 401 is not a 502), with Cloudflare's message.
func upstreamError(op string, resp *http.Response, body []byte) error {
	if rl := ratelimit.Classify(op, resp.StatusCode, resp.Header, body); rl != nil {
		return rl
	}
	return derrors.New(catalog.StatusCode(resp.StatusCode), op, workersAIErrorMessage(body, "workers ai error")).
		With("status", strconv.Itoa(resp.StatusCode)).
		With("body", catalog.Truncate(string(body), errorBodyMax))
}
