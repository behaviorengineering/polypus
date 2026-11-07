package batch

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// Batch endpoints a batch input line can target.
const (
	EndpointChatCompletions = "/v1/chat/completions"
	EndpointEmbeddings      = "/v1/embeddings"
)

// InputLine is one OpenAI Batch JSONL request line.
type InputLine struct {
	CustomID string          `json:"custom_id"`
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Body     json.RawMessage `json:"body"`
}

// ParsedInput is validated batch input, ready for any provider's submitter
// (Cloudflare, OpenRouter or Claude).
type ParsedInput struct {
	Endpoint string
	Model    string
	Lines    []InputLine
}

const opParseBatchJSONL = "batch.ParseBatchJSONL"

// ParseBatchJSONL validates OpenAI batch input for chat or embeddings.
func ParseBatchJSONL(raw []byte, endpoint string) (ParsedInput, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint != EndpointChatCompletions && endpoint != EndpointEmbeddings {
		return ParsedInput{}, derrors.New(derrors.CodeInvalid, opParseBatchJSONL, "unsupported endpoint").
			With("endpoint", endpoint)
	}
	lines := splitJSONL(raw)
	if len(lines) == 0 {
		return ParsedInput{}, derrors.New(derrors.CodeInvalid, opParseBatchJSONL, "empty input")
	}
	out := ParsedInput{Endpoint: endpoint}
	// Results are matched back by custom_id, so it must be unique.
	seen := make(map[string]struct{}, len(lines))
	for i, line := range lines {
		lineNo := strconv.Itoa(i + 1)
		invalid := func(msg string, cause error) *derrors.Error {
			return derrors.Wrap(cause, derrors.CodeInvalid, opParseBatchJSONL, msg).With("line", lineNo)
		}
		var req InputLine
		if err := json.Unmarshal(line, &req); err != nil {
			return ParsedInput{}, invalid("parse line", err)
		}
		req.CustomID = strings.TrimSpace(req.CustomID)
		if req.CustomID == "" {
			return ParsedInput{}, invalid("custom_id required", nil)
		}
		if strings.ToUpper(strings.TrimSpace(req.Method)) != "POST" {
			return ParsedInput{}, invalid("method must be POST", nil)
		}
		if strings.TrimSpace(req.URL) != endpoint {
			return ParsedInput{}, invalid("line url must match batch endpoint", nil)
		}
		lineModel, err := extractModelFromBody(req.Body)
		if err != nil {
			return ParsedInput{}, invalid("model in body", err)
		}
		if out.Model == "" {
			out.Model = lineModel
		} else if out.Model != lineModel {
			return ParsedInput{}, invalid("all lines must use the same model", nil).With("model", lineModel)
		}
		if err := validateBodyForEndpoint(endpoint, req.Body); err != nil {
			return ParsedInput{}, invalid("body", err)
		}
		if _, dup := seen[req.CustomID]; dup {
			return ParsedInput{}, invalid("duplicate custom_id", nil).With("custom_id", req.CustomID)
		}
		seen[req.CustomID] = struct{}{}
		out.Lines = append(out.Lines, req)
	}
	return out, nil
}

func splitJSONL(raw []byte) [][]byte {
	var out [][]byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		out = append(out, line)
	}
	return out
}

func extractModelFromBody(body json.RawMessage) (string, error) {
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", err
	}
	model := strings.TrimSpace(probe.Model)
	if model == "" {
		return "", derrors.New(derrors.CodeInvalid, "batch.extractModelFromBody", "model required")
	}
	return model, nil
}

func validateBodyForEndpoint(endpoint string, body json.RawMessage) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return err
	}
	if stream, ok := m["stream"]; ok {
		var b bool
		if err := json.Unmarshal(stream, &b); err == nil && b {
			return derrors.New(derrors.CodeInvalid, "batch.validateBodyForEndpoint", "stream not supported in batch")
		}
	}
	switch endpoint {
	case EndpointChatCompletions:
		if _, ok := m["messages"]; !ok {
			return derrors.New(derrors.CodeInvalid, "batch.validateBodyForEndpoint", "messages required")
		}
	case EndpointEmbeddings:
		if _, ok := m["input"]; !ok {
			return derrors.New(derrors.CodeInvalid, "batch.validateBodyForEndpoint", "input required")
		}
	}
	return nil
}
