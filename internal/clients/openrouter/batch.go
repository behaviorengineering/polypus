// Package openrouter is the OpenRouter Batch API client. Chat and embeddings themselves go
// through Bifrost; Bifrost's OpenRouter provider does not support batches.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const (
	defaultAPIBase = "https://openrouter.ai/api/v1"
	batchMaxBody   = 64 << 20
	// completionWindow is the only window OpenRouter accepts.
	completionWindow = "24h"
)

// BatchStatus is a batch's state as OpenRouter reports it.
type BatchStatus string

// Batch status values OpenRouter reports.
const (
	StatusValidating BatchStatus = "validating"
	StatusInProgress BatchStatus = "in_progress"
	StatusFinalizing BatchStatus = "finalizing"
	StatusCompleted  BatchStatus = "completed"
	StatusFailed     BatchStatus = "failed"
	StatusExpired    BatchStatus = "expired"
	StatusCancelled  BatchStatus = "cancelled"
)

// Batch is the OpenRouter batch object. Results are inline once the batch completes.
type Batch struct {
	ID            string        `json:"id"`
	Status        BatchStatus   `json:"status"`
	RequestCounts RequestCounts `json:"request_counts"`
	Results       []BatchResult `json:"results"`
	Error         *BatchError   `json:"error"`
}

// RequestCounts mirrors the batch request_counts.
type RequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// BatchError is a batch-level failure.
type BatchError struct {
	Message string `json:"message"`
}

// BatchResult is one finished request. Response.Body has the sync endpoint's response shape.
type BatchResult struct {
	CustomID string          `json:"custom_id"`
	Response *BatchResponse  `json:"response"`
	Error    *BatchItemError `json:"error"`
}

// BatchResponse is a finished request's HTTP status and body.
type BatchResponse struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}

// BatchItemError is a failed request's error.
type BatchItemError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// submitBody is the create body. Requests must stay the last field: OpenRouter returns 400
// when requests comes before endpoint, model and completion_window.
type submitBody struct {
	Endpoint         string       `json:"endpoint"`
	Model            string       `json:"model"`
	CompletionWindow string       `json:"completion_window"`
	Requests         []submitItem `json:"requests"`
}

type submitItem struct {
	CustomID string          `json:"custom_id"`
	Body     json.RawMessage `json:"body"`
}

// Client calls the OpenRouter Batch API.
type Client struct {
	apiBase    string
	apiKey     string
	httpClient *http.Client
	backendID  string             // scopes the outbound breaker to this backend.
	policies   *outbound.Policies // nil: the process default.
}

// NewClient builds a batch client from an OpenRouter backend definition.
func NewClient(b config.BackendDef) (*Client, error) {
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	return newClient(apiBase(b.BaseURL), token)
}

func newClient(base, token string) (*Client, error) {
	if strings.TrimSpace(token) == "" {
		return nil, derrors.New(derrors.CodeInvalid, "openrouter.newClient", "api key required")
	}
	return &Client{
		apiBase: base,
		apiKey:  token,
		// No client Timeout: the caller's ctx deadline bounds each call (outbound fails
		// closed without one).
		httpClient: &http.Client{
			Transport: observability.WrapTransport(http.DefaultTransport),
		},
	}, nil
}

// apiBase resolves the API root (the backend base_url, which ends in /api/v1).
func apiBase(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return defaultAPIBase
	}
	return base
}

// Clients caches one batch client per backend, credential and URL. A gateway owns
// one; its clients share the gateway's outbound policies.
type Clients struct {
	Policies *outbound.Policies
	reg      catalog.Registry[Client]
}

// Get returns the cached client for b, building it on first use.
func (cs *Clients) Get(b config.BackendDef) (*Client, error) {
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	base := apiBase(b.BaseURL)
	return cs.reg.Get(b.ID+"\x00"+token+"\x00"+base, func() (*Client, error) {
		c, err := newClient(base, token)
		if err != nil {
			return nil, err
		}
		c.backendID = b.ID
		c.policies = cs.Policies
		return c, nil
	})
}

// SubmitBatch creates a batch of endpoint requests for model (the bare OpenRouter slug) and
// returns its id. Each body drops model and stream: the batch sets the model, and batches
// do not stream. A create is not retried, so it can never submit the same batch twice.
func (c *Client) SubmitBatch(ctx context.Context, model, endpoint string, lines []batch.InputLine) (string, error) {
	if c == nil {
		return "", derrors.New(derrors.CodeFailedPrecondition, "openrouter.SubmitBatch", "not configured")
	}
	items := make([]submitItem, 0, len(lines))
	for _, line := range lines {
		body, err := batchItemBody(line.Body)
		if err != nil {
			return "", derrors.Wrap(err, derrors.CodeInvalid, "openrouter.SubmitBatch", "line body").
				With("custom_id", line.CustomID)
		}
		items = append(items, submitItem{CustomID: line.CustomID, Body: body})
	}
	payload, err := json.Marshal(submitBody{Endpoint: endpoint, Model: model, CompletionWindow: completionWindow, Requests: items})
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, "openrouter.SubmitBatch", "marshal")
	}
	raw, err := c.do(ctx, http.MethodPost, c.apiBase+"/batches", payload)
	if err != nil {
		return "", err
	}
	var created Batch
	if err := json.Unmarshal(raw, &created); err != nil || strings.TrimSpace(created.ID) == "" {
		return "", derrors.New(derrors.CodeUnavailable, "openrouter.SubmitBatch", "batch id missing from response")
	}
	return created.ID, nil
}

// GetBatch returns the batch, with inline results once it has completed.
func (c *Client) GetBatch(ctx context.Context, id string) (Batch, error) {
	if c == nil {
		return Batch{}, derrors.New(derrors.CodeFailedPrecondition, "openrouter.GetBatch", "not configured")
	}
	raw, err := c.do(ctx, http.MethodGet, c.apiBase+"/batches/"+url.PathEscape(strings.TrimSpace(id)), nil)
	if err != nil {
		return Batch{}, err
	}
	var b Batch
	if err := json.Unmarshal(raw, &b); err != nil {
		return Batch{}, derrors.Wrap(err, derrors.CodeUnavailable, "openrouter.GetBatch", "decode")
	}
	return b, nil
}

func batchItemBody(raw json.RawMessage) (json.RawMessage, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	delete(body, "model")
	delete(body, "stream")
	return json.Marshal(body)
}

func (c *Client) do(ctx context.Context, method, target string, payload []byte) ([]byte, error) {
	if ctx == nil {
		return nil, derrors.New(derrors.CodeInvalid, "openrouter.do", "context required")
	}
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "openrouter.do", "request")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// A create is sent once (never submit a batch twice); a retrieve may retry.
	send := c.policies.Do
	if method != http.MethodGet {
		send = c.policies.DoOnce
	}
	resp, err := send(ctx, outbound.Scoped(outbound.DepOpenRouter, c.backendID), c.httpClient, req)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "openrouter.do", strings.ToLower(method)).
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, batchMaxBody))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "openrouter.do", "read body")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, derrors.New(catalog.StatusCode(resp.StatusCode), "openrouter.do", errorMessage(raw, resp.StatusCode)).
			With("status", strconv.Itoa(resp.StatusCode))
	}
	return raw, nil
}

func errorMessage(raw []byte, status int) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.Error.Message) != "" {
		return env.Error.Message
	}
	return fmt.Sprintf("openrouter batch: status %d: %s", status, catalog.Truncate(string(raw), 200))
}
