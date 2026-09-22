package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const systemOneTimeout = 120 * time.Second

// SystemOne calls Workers AI typesafe/jev (or another systemone-capable model).
// Cloudflare's REST surface for this third-party model is POST /ai/run with
// body {"model":"<id>","input":{...}}, not /ai/run/{model} with a bare payload
// (that returns "No route for that URI"). Response is unwrapped from the
// Workers AI {success, result, errors} envelope when present.
func (c *Client) SystemOne(ctx context.Context, model string, body []byte) ([]byte, error) {
	if c == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "cloudflare.SystemOne", "not configured")
	}
	model = NormalizeModel(strings.TrimSpace(model))
	if model == "" {
		return nil, derrors.New(derrors.CodeInvalid, "cloudflare.SystemOne", "model required")
	}
	input, err := stripModelField(body)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "cloudflare.SystemOne", "request body")
	}
	payload, err := json.Marshal(struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}{Model: model, Input: input})
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "cloudflare.SystemOne", "marshal run body")
	}

	target := runEndpointURL(c.apiBase)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "cloudflare.SystemOne", "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.doSystemOne(ctx, httpReq)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.SystemOne", "post /run").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.SystemOne", "read body")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := workersAIErrorMessage(raw)
		return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.SystemOne", msg).
			With("status", strconv.Itoa(resp.StatusCode)).
			With("body", truncate(string(raw), 256))
	}
	return unwrapRunResult(raw)
}

func (c *Client) doSystemOne(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c == nil || c.speechClient == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "cloudflare.doSystemOne", "not configured")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, systemOneTimeout)
		defer cancel()
		req = req.WithContext(ctx)
	}
	return c.speechClient.Do(req)
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
		return nil, err
	}
	delete(m, "model")
	return json.Marshal(m)
}

// unwrapRunResult returns the TypeSafe {model, answers, usage} payload.
// Workers AI may wrap it as:
//   - {success, result, errors} (classic envelope)
//   - {state, result, gatewayMetadata, model} (Unified gateway; answers under result)
// Bare TypeSafe payloads pass through.
func unwrapRunResult(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.unwrapRunResult", "empty response")
	}
	var envelope struct {
		Success *bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
		State  string          `json:"state"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.unwrapRunResult", "parse json")
	}
	if envelope.Success != nil {
		if !*envelope.Success {
			msg := "cloudflare workers ai error"
			if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
				msg = envelope.Errors[0].Message
			}
			return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.unwrapRunResult", msg)
		}
		if len(envelope.Result) > 0 {
			return envelope.Result, nil
		}
		return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.unwrapRunResult", "empty result")
	}
	// Unified gateway: {state, result:{answers,...}, gatewayMetadata, model}
	if len(envelope.Result) > 0 && looksLikeTypeSafeResult(envelope.Result) {
		return envelope.Result, nil
	}
	// Bare TypeSafe response (no Workers AI / Unified envelope).
	return raw, nil
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

// workersAIErrorMessage prefers Cloudflare's errors[0].message when present.
func workersAIErrorMessage(raw []byte) string {
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Errors) > 0 {
		if msg := strings.TrimSpace(envelope.Errors[0].Message); msg != "" {
			return msg
		}
	}
	return "workers ai error"
}
