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

	"github.com/behaviorengineering/polypus/internal/batch"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const (
	batchMaxPayload = 10 << 20
	batchHopTimeout = 120 * time.Second
)

// BatchPollState is the Workers AI async batch poll outcome.
type BatchPollState int

const (
	BatchPollQueued    BatchPollState = 1
	BatchPollRunning   BatchPollState = 2
	BatchPollCompleted BatchPollState = 3
	BatchPollFailed    BatchPollState = 4
)

// BatchResponseItem is one line result from Cloudflare batch poll.
type BatchResponseItem struct {
	ExternalReference string
	Success           bool
	Result            json.RawMessage
	ErrorMessage      string
}

// BatchPollResult is the outcome of polling a queued batch.
type BatchPollResult struct {
	State     BatchPollState
	Responses []BatchResponseItem
	Usage     json.RawMessage
}

var batchAllowedModels = map[string]struct{}{
	"@cf/google/gemma-4-26b-a4b-it":            {},
	"@cf/meta/llama-3.3-70b-instruct-fp8-fast": {},
	"@cf/meta/llama-4-scout-17b-16e-instruct":  {},
	"@cf/openai/gpt-oss-120b":                  {},
	"@cf/openai/gpt-oss-20b":                   {},
	"@cf/qwen/qwen3-30b-a3b-fp8":               {},
	"@cf/qwen/qwen3.8-27b":                     {},
	"@cf/deepseek-ai/deepseek-v4-flash-0731":   {},
	"@cf/moonshotai/kimi-k2.6":                 {},
	"@cf/baai/bge-base-en-v1.5":                {},
	"@cf/baai/bge-large-en-v1.5":               {},
	"@cf/baai/bge-small-en-v1.5":               {},
}

// ModelSupportsBatch reports whether Workers AI documents batch for the downstream model id.
func ModelSupportsBatch(model string) bool {
	model = NormalizeModel(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	_, ok := batchAllowedModels[model]
	return ok
}

// SubmitBatch queues an async Workers AI batch for the given model.
func (c *Client) SubmitBatch(ctx context.Context, model string, endpoint string, lines []batch.InputLine) (string, error) {
	if c == nil {
		return "", derrors.New(derrors.CodeFailedPrecondition, "cloudflare.SubmitBatch", "not configured")
	}
	model = NormalizeModel(strings.TrimSpace(model))
	if model == "" {
		return "", derrors.New(derrors.CodeInvalid, "cloudflare.SubmitBatch", "model required")
	}
	if !ModelSupportsBatch(model) {
		return "", derrors.New(derrors.CodeInvalid, "cloudflare.SubmitBatch", "model does not support batch").
			With("model", model)
	}
	requests, err := translateBatchRequests(endpoint, lines)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]interface{}{
		"requests": requests,
	})
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, "cloudflare.SubmitBatch", "marshal")
	}
	if len(payload) > batchMaxPayload {
		return "", derrors.New(derrors.CodeInvalid, "cloudflare.SubmitBatch", "batch payload exceeds 10MB limit")
	}
	raw, err := c.postBatchRun(ctx, model, payload)
	if err != nil {
		return "", err
	}
	return parseSubmitRequestID(raw)
}

// PollBatch checks status or fetches completed responses for a queued batch.
func (c *Client) PollBatch(ctx context.Context, model, cfRequestID string) (BatchPollResult, error) {
	if c == nil {
		return BatchPollResult{}, derrors.New(derrors.CodeFailedPrecondition, "cloudflare.PollBatch", "not configured")
	}
	model = NormalizeModel(strings.TrimSpace(model))
	cfRequestID = strings.TrimSpace(cfRequestID)
	if model == "" || cfRequestID == "" {
		return BatchPollResult{}, derrors.New(derrors.CodeInvalid, "cloudflare.PollBatch", "model and request_id required")
	}
	payload, err := json.Marshal(map[string]string{
		"request_id": cfRequestID,
	})
	if err != nil {
		return BatchPollResult{}, derrors.Wrap(err, derrors.CodeInternal, "cloudflare.PollBatch", "marshal")
	}
	raw, err := c.postBatchRun(ctx, model, payload)
	if err != nil {
		return BatchPollResult{}, err
	}
	return parsePollResult(raw)
}

func (c *Client) postBatchRun(ctx context.Context, model string, payload []byte) ([]byte, error) {
	target := batchRunURL(c.apiBase, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "cloudflare.postBatchRun", "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, batchHopTimeout)
		defer cancel()
		httpReq = httpReq.WithContext(ctx)
	}
	resp, err := c.speechClient.Do(httpReq)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.postBatchRun", "post").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, modelsMaxBody))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.postBatchRun", "read body")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if rl := ClassifyRateLimit("cloudflare.postBatchRun", resp.StatusCode, resp.Header, raw); rl != nil {
			return nil, rl
		}
		msg := workersAIErrorMessage(raw)
		return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.postBatchRun", msg).
			With("status", strconv.Itoa(resp.StatusCode)).
			With("body", truncate(string(raw), 256))
	}
	return unwrapBatchEnvelope(raw)
}

func batchRunURL(apiBase, model string) string {
	return runURL(apiBase, model) + "?queueRequest=true"
}

func translateBatchRequests(endpoint string, lines []batch.InputLine) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(lines))
	for _, line := range lines {
		var item map[string]interface{}
		var err error
		switch endpoint {
		case batch.EndpointChatCompletions:
			item, err = chatLineToCF(line.Body, line.CustomID)
		case batch.EndpointEmbeddings:
			item, err = embedLineToCF(line.Body, line.CustomID)
		default:
			return nil, derrors.New(derrors.CodeInvalid, "cloudflare.translateBatchRequests", "unsupported endpoint")
		}
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func chatLineToCF(body json.RawMessage, customID string) (map[string]interface{}, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "cloudflare.chatLineToCF", "parse body")
	}
	delete(m, "model")
	delete(m, "stream")
	m["external_reference"] = customID
	return m, nil
}

func embedLineToCF(body json.RawMessage, customID string) (map[string]interface{}, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "cloudflare.embedLineToCF", "parse body")
	}
	delete(m, "model")
	input := m["input"]
	delete(m, "input")
	text, err := embeddingInputToText(input)
	if err != nil {
		return nil, err
	}
	m["text"] = text
	m["external_reference"] = customID
	return m, nil
}

func embeddingInputToText(input interface{}) (string, error) {
	switch v := input.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return "", derrors.New(derrors.CodeInvalid, "cloudflare.embeddingInputToText", "input required")
		}
		return v, nil
	case []interface{}:
		if len(v) != 1 {
			return "", derrors.New(derrors.CodeInvalid, "cloudflare.embeddingInputToText", "batch embeddings require a single input per line")
		}
		s, ok := v[0].(string)
		if !ok || strings.TrimSpace(s) == "" {
			return "", derrors.New(derrors.CodeInvalid, "cloudflare.embeddingInputToText", "input must be a string")
		}
		return s, nil
	default:
		return "", derrors.New(derrors.CodeInvalid, "cloudflare.embeddingInputToText", "unsupported input type")
	}
}

func parseSubmitRequestID(raw []byte) (string, error) {
	var envelope struct {
		Result struct {
			RequestID string `json:"request_id"`
			Status    string `json:"status"`
		} `json:"result"`
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.parseSubmitRequestID", "parse")
	}
	id := strings.TrimSpace(envelope.Result.RequestID)
	if id == "" {
		id = strings.TrimSpace(envelope.RequestID)
	}
	if id == "" {
		return "", derrors.New(derrors.CodeUnavailable, "cloudflare.parseSubmitRequestID", "missing request_id")
	}
	return id, nil
}

type cfBatchResponseRow struct {
	ExternalReference string          `json:"external_reference"`
	Success           bool            `json:"success"`
	Result            json.RawMessage `json:"result"`
	Error             json.RawMessage `json:"error"`
}

func parsePollResult(raw []byte) (BatchPollResult, error) {
	var envelope struct {
		Result struct {
			Status    string               `json:"status"`
			Responses []cfBatchResponseRow `json:"responses"`
			Usage     json.RawMessage      `json:"usage"`
		} `json:"result"`
		Status    string               `json:"status"`
		Responses []cfBatchResponseRow `json:"responses"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return BatchPollResult{}, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.parsePollResult", "parse")
	}
	status := strings.ToLower(strings.TrimSpace(envelope.Result.Status))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(envelope.Status))
	}
	responses := envelope.Result.Responses
	if len(responses) == 0 {
		responses = envelope.Responses
	}
	if len(responses) > 0 {
		out := BatchPollResult{State: BatchPollCompleted, Usage: envelope.Result.Usage}
		for _, r := range responses {
			out.Responses = append(out.Responses, BatchResponseItem{
				ExternalReference: r.ExternalReference,
				Success:           r.Success,
				Result:            r.Result,
				ErrorMessage:      cfBatchRowErrorMessage(r.Error),
			})
		}
		return out, nil
	}
	switch status {
	case "queued":
		return BatchPollResult{State: BatchPollQueued}, nil
	case "running", "in_progress":
		return BatchPollResult{State: BatchPollRunning}, nil
	case "failed", "error", "cancelled":
		return BatchPollResult{State: BatchPollFailed}, nil
	default:
		if status != "" {
			return BatchPollResult{}, derrors.New(derrors.CodeUnavailable, "cloudflare.parsePollResult", "unexpected batch status").
				With("status", status)
		}
		return BatchPollResult{}, derrors.New(derrors.CodeUnavailable, "cloudflare.parsePollResult", "unexpected poll response")
	}
}

func cfBatchRowErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && strings.TrimSpace(obj.Message) != "" {
		return strings.TrimSpace(obj.Message)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}

func unwrapBatchEnvelope(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	var envelope struct {
		Success *bool           `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return raw, nil
	}
	if envelope.Success != nil && !*envelope.Success {
		msg := workersAIErrorMessage(raw)
		return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.unwrapBatchEnvelope", msg)
	}
	if len(envelope.Result) > 0 {
		return envelope.Result, nil
	}
	return raw, nil
}
