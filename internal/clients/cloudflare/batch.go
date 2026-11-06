package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const (
	batchMaxPayload = 10 << 20
)

// Op names for the batch errors.
const (
	opSubmitBatch  = "cloudflare.SubmitBatch"
	opPollBatch    = "cloudflare.PollBatch"
	opPostBatchRun = "cloudflare.postBatchRun"
	opEmbedInput   = "cloudflare.embeddingInputToText"
)

// BatchPollState is the Workers AI async batch poll outcome.
type BatchPollState int

// Workers AI async batch states a poll reports.
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
	if ctx == nil {
		return "", derrors.New(derrors.CodeInvalid, opSubmitBatch, "context required")
	}
	if c == nil {
		return "", derrors.New(derrors.CodeFailedPrecondition, opSubmitBatch, "not configured")
	}
	model = NormalizeModel(strings.TrimSpace(model))
	if model == "" {
		return "", derrors.New(derrors.CodeInvalid, opSubmitBatch, "model required")
	}
	if !ModelSupportsBatch(model) {
		return "", derrors.New(derrors.CodeInvalid, opSubmitBatch, "model does not support batch").
			With("model", model)
	}
	requests, err := translateBatchRequests(endpoint, lines)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(struct {
		Requests []map[string]json.RawMessage `json:"requests"`
	}{requests})
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInternal, opSubmitBatch, "marshal")
	}
	if len(payload) > batchMaxPayload {
		return "", derrors.New(derrors.CodeInvalid, opSubmitBatch, "batch payload exceeds 10MB limit")
	}
	raw, err := c.postBatchRun(ctx, model, payload, false)
	if err != nil {
		return "", err
	}
	return parseSubmitRequestID(raw)
}

// PollBatch checks status or fetches completed responses for a queued batch. A poll
// is idempotent, so a transient failure is retried within ctx's deadline.
func (c *Client) PollBatch(ctx context.Context, model, cfRequestID string) (BatchPollResult, error) {
	if ctx == nil {
		return BatchPollResult{}, derrors.New(derrors.CodeInvalid, opPollBatch, "context required")
	}
	if c == nil {
		return BatchPollResult{}, derrors.New(derrors.CodeFailedPrecondition, opPollBatch, "not configured")
	}
	model = NormalizeModel(strings.TrimSpace(model))
	cfRequestID = strings.TrimSpace(cfRequestID)
	if model == "" || cfRequestID == "" {
		return BatchPollResult{}, derrors.New(derrors.CodeInvalid, opPollBatch, "model and request_id required")
	}
	payload, err := json.Marshal(map[string]string{
		"request_id": cfRequestID,
	})
	if err != nil {
		return BatchPollResult{}, derrors.Wrap(err, derrors.CodeInternal, opPollBatch, "marshal")
	}
	raw, err := c.postBatchRun(ctx, model, payload, true)
	if err != nil {
		return BatchPollResult{}, err
	}
	return parsePollResult(raw)
}

// postBatchRun posts payload to the async batch /run endpoint. A submit (retry false)
// goes once, so it can never queue the same paid batch twice; a poll (retry true)
// goes under the retry policy.
func (c *Client) postBatchRun(ctx context.Context, model string, payload []byte, retry bool) ([]byte, error) {
	target := batchRunURL(c.apiBase, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, opPostBatchRun, "request")
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.run(ctx, opPostBatchRun, httpReq, retry)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := catalog.ReadCapped(resp.Body, runMaxBody, opPostBatchRun)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamError(opPostBatchRun, resp, raw)
	}
	return unwrapBatchEnvelope(raw)
}

func batchRunURL(apiBase, model string) string {
	return runURL(apiBase, model) + "?queueRequest=true"
}

// translateBatchRequests turns OpenAI batch lines into Workers AI batch requests.
// Bodies stay as raw JSON, so fields the gateway does not touch (large integers such
// as seed included) pass through byte for byte.
func translateBatchRequests(endpoint string, lines []batch.InputLine) ([]map[string]json.RawMessage, error) {
	out := make([]map[string]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		var item map[string]json.RawMessage
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

func chatLineToCF(body json.RawMessage, customID string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "cloudflare.chatLineToCF", "parse body")
	}
	delete(m, "model")
	delete(m, "stream")
	return withString(m, "external_reference", customID)
}

func embedLineToCF(body json.RawMessage, customID string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
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
	if m, err = withString(m, "text", text); err != nil {
		return nil, err
	}
	return withString(m, "external_reference", customID)
}

// withString sets key to the JSON string s.
func withString(m map[string]json.RawMessage, key, s string) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "cloudflare.withString", "marshal").With("key", key)
	}
	m[key] = raw
	return m, nil
}

// embeddingInputToText reads a batch embedding line's input: a string, or a list of
// exactly one string. Workers AI batch takes one text per line.
func embeddingInputToText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", derrors.New(derrors.CodeInvalid, opEmbedInput, "input required")
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 {
			return "", derrors.New(derrors.CodeInvalid, opEmbedInput, "batch embeddings require a single string input per line")
		}
		text = list[0]
	}
	if strings.TrimSpace(text) == "" {
		return "", derrors.New(derrors.CodeInvalid, opEmbedInput, "input required")
	}
	return text, nil
}

// parseSubmitRequestID reads the request id from a submit result (postBatchRun has
// already removed the {success, result} envelope).
func parseSubmitRequestID(raw []byte) (string, error) {
	var result struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.parseSubmitRequestID", "parse")
	}
	id := strings.TrimSpace(result.RequestID)
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

// parsePollResult reads a poll result (postBatchRun has already removed the
// {success, result} envelope): its responses once done, else its status.
func parsePollResult(raw []byte) (BatchPollResult, error) {
	var result struct {
		Status    string               `json:"status"`
		Responses []cfBatchResponseRow `json:"responses"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return BatchPollResult{}, derrors.Wrap(err, derrors.CodeUnavailable, "cloudflare.parsePollResult", "parse")
	}
	status := strings.ToLower(strings.TrimSpace(result.Status))
	if len(result.Responses) > 0 {
		out := BatchPollResult{State: BatchPollCompleted}
		for _, r := range result.Responses {
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
	case "queued", "":
		return BatchPollResult{State: BatchPollQueued}, nil
	case "running", "in_progress":
		return BatchPollResult{State: BatchPollRunning}, nil
	case "failed", "error", "cancelled":
		return BatchPollResult{State: BatchPollFailed}, nil
	default:
		return BatchPollResult{}, derrors.New(derrors.CodeUnavailable, "cloudflare.parsePollResult", "unexpected batch status").
			With("status", status)
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
		Success *bool            `json:"success"`
		Result  json.RawMessage  `json:"result"`
		Errors  []workersAIError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		// Not an envelope: Workers AI can answer with the bare result, which the
		// caller's own parser reads (and reports if it is malformed).
		return raw, nil
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, derrors.New(derrors.CodeUnavailable, "cloudflare.unwrapBatchEnvelope", firstErrorMessage(envelope.Errors, "workers ai error"))
	}
	if len(envelope.Result) > 0 {
		return envelope.Result, nil
	}
	return raw, nil
}
