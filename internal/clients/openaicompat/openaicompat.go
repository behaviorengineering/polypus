// Package openaicompat lists models from, and pings, OpenAI-compatible backends: local
// servers, hosted APIs such as DeepInfra, OpenRouter's models API and Switchyard.
// Cloudflare, Gemini and Anthropic have their own catalog clients.
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// modelsMaxBody caps a model list response.
const modelsMaxBody = 4 << 20

// Client calls OpenAI-compatible backends. The zero value is not usable; set both fields.
type Client struct {
	// HTTP carries the calls. It sets no Timeout: the caller's ctx deadline bounds
	// each call, and the policies fail closed without one.
	HTTP *http.Client
	// Policies retries and breaks the calls.
	Policies *outbound.Policies
}

// Model is one entry of an OpenAI model list.
type Model struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ListModels GETs the model list at target. auth, when set, is sent as the
// Authorization header. It accepts the OpenAI {"object":"list","data":[...]} shape and
// a bare array, and drops entries without an id.
func (c Client) ListModels(ctx context.Context, target, auth string) ([]Model, error) {
	const op = "openaicompat.ListModels"
	if err := c.check(op); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "request")
	}
	req.Header.Set("Accept", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.Policies.Do(ctx, outbound.DepGateway, c.HTTP, req)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "get")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, derrors.New(derrors.CodeUnavailable, op, "models list status").
			With("status", strconv.Itoa(resp.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody+1))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, op, "read body")
	}
	if len(raw) > modelsMaxBody {
		return nil, derrors.New(derrors.CodeUnavailable, op, "models list too large")
	}
	return ParseModels(raw), nil
}

// ParseModels reads an OpenAI model list body, or a bare array of models. A body that
// is neither yields no models.
func ParseModels(raw []byte) []Model {
	var list struct {
		Object string  `json:"object"`
		Data   []Model `json:"data"`
	}
	if err := json.Unmarshal(raw, &list); err == nil && (list.Object == "list" || len(list.Data) > 0) {
		return withIDs(list.Data)
	}
	var arr []Model
	if err := json.Unmarshal(raw, &arr); err == nil {
		return withIDs(arr)
	}
	return nil
}

func withIDs(in []Model) []Model {
	out := make([]Model, 0, len(in))
	for _, m := range in {
		m.ID = strings.TrimSpace(m.ID)
		if m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}

// Ping GETs target and fails only on a 5xx: any other answer shows the backend is up.
func (c Client) Ping(ctx context.Context, target string) error {
	return c.probe(ctx, "openaicompat.Ping", target, func(status int) bool { return status < 500 })
}

// CheckHealth GETs a health endpoint and fails unless it answers 2xx.
func (c Client) CheckHealth(ctx context.Context, target string) error {
	return c.probe(ctx, "openaicompat.CheckHealth", target, func(status int) bool { return status >= 200 && status < 300 })
}

func (c Client) probe(ctx context.Context, op, target string, healthy func(status int) bool) error {
	if err := c.check(op); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInvalid, op, "request")
	}
	resp, err := c.Policies.Do(ctx, outbound.DepHealth, c.HTTP, req)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnavailable), op, "get")
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, modelsMaxBody)); err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, op, "drain body")
	}
	if !healthy(resp.StatusCode) {
		return derrors.New(derrors.CodeUnavailable, op, "unhealthy status").
			With("status", strconv.Itoa(resp.StatusCode))
	}
	return nil
}

func (c Client) check(op string) error {
	if c.HTTP == nil || c.Policies == nil {
		return derrors.New(derrors.CodeInternal, op, "client needs an HTTP client and policies")
	}
	return nil
}
