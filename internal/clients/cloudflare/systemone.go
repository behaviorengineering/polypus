package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/httpx"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

// Op names for the SystemOne errors.
const (
	opSystemOne       = "cloudflare.SystemOne"
	opUnwrapRunResult = "cloudflare.unwrapRunResult"
)

// SystemOne calls Workers AI typesafe/jev (or another systemone-capable model).
// Cloudflare's REST surface for this third-party model is POST /ai/run with
// body {"model":"<id>","input":{...}}, not /ai/run/{model} with a bare payload
// (that returns "No route for that URI"). Response is unwrapped from the
// Workers AI {success, result, errors} envelope when present.
func (c *Client) SystemOne(ctx context.Context, model string, body []byte) ([]byte, error) {
	if ctx == nil {
		return nil, derrors.New(derrors.CodeInvalid, opSystemOne, "context required")
	}
	if c == nil {
		return nil, derrors.New(derrors.CodeNotReady, opSystemOne, "not configured")
	}
	model = NormalizeModel(strings.TrimSpace(model))
	if model == "" {
		return nil, derrors.New(derrors.CodeInvalid, opSystemOne, "model required")
	}
	input, err := stripModelField(body)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, opSystemOne, "request body")
	}
	payload, err := json.Marshal(struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}{Model: model, Input: input})
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, opSystemOne, "marshal run body")
	}

	target := runEndpointURL(c.apiBase)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, opSystemOne, "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.runOnce(ctx, opSystemOne, httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := httpx.ReadCapped(resp.Body, runMaxBody, opSystemOne)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamError(opSystemOne, resp, raw)
	}
	return unwrapRunResult(raw)
}

// stripModelField removes top-level "model" from a JSON object so CF /run
// input receives only state + questions (and any other TypeSafe fields).
func stripModelField(body []byte) ([]byte, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, derrors.New(derrors.CodeInvalid, "cloudflare.stripModelField", "empty body")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "cloudflare.stripModelField", "parse body")
	}
	delete(m, "model")
	return json.Marshal(m)
}

// unwrapRunResult returns the TypeSafe {model, answers, usage} payload.
// Workers AI may wrap it as one or both of:
//   - {success, result, errors} (classic envelope)
//   - {state, result, gatewayMetadata, model} (Unified gateway; answers under result)
//
// Live CF responses for typesafe/jev currently nest Unified inside classic
// success.result, so we peel until answers are top-level.
// Bare TypeSafe payloads pass through.
func unwrapRunResult(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, derrors.New(derrors.CodeUnavailable, opUnwrapRunResult, "empty response")
	}
	cur := raw
	for range 3 {
		var envelope struct {
			Success *bool            `json:"success"`
			Errors  []workersAIError `json:"errors"`
			Result  json.RawMessage  `json:"result"`
		}
		if err := json.Unmarshal(cur, &envelope); err != nil {
			return nil, derrors.Wrap(err, derrors.CodeUnavailable, opUnwrapRunResult, "parse json")
		}
		if looksLikeTypeSafeResult(cur) {
			return cur, nil
		}
		if envelope.Success != nil {
			if !*envelope.Success {
				if rl, ok := ratelimit.Classify(opUnwrapRunResult, 0, nil, cur); ok {
					return nil, rl
				}
				return nil, derrors.New(derrors.CodeUnavailable, opUnwrapRunResult, firstErrorMessage(envelope.Errors, "cloudflare workers ai error"))
			}
			if len(envelope.Result) == 0 {
				return nil, derrors.New(derrors.CodeUnavailable, opUnwrapRunResult, "empty result")
			}
			cur = envelope.Result
			continue
		}
		// Unified gateway (no success flag): peel result when it looks TypeSafe
		// or itself wraps further.
		if len(envelope.Result) > 0 {
			cur = envelope.Result
			continue
		}
		break
	}
	// Only a TypeSafe payload is an answer: after three peels, or an object with
	// neither success nor result, what is left ({} or a nested envelope) is not one.
	if !looksLikeTypeSafeResult(cur) {
		return nil, derrors.New(derrors.CodeUnavailable, opUnwrapRunResult, "no answers in response").
			With("body", httpx.Truncate(string(cur), errorBodyMax))
	}
	return cur, nil
}

func looksLikeTypeSafeResult(raw json.RawMessage) bool {
	var probe struct {
		Answers json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return len(bytes.TrimSpace(probe.Answers)) > 0 && string(bytes.TrimSpace(probe.Answers)) != "null"
}

func runEndpointURL(apiBase string) string {
	return strings.TrimRight(apiBase, "/") + "/run"
}
