package batch

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

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

// ParsedInput is validated batch input ready for Cloudflare translation.
type ParsedInput struct {
	Endpoint    string
	Model       string
	Lines       []InputLine
	BodyByID    map[string]json.RawMessage
	CustomOrder []string
}

// ParseBatchJSONL validates OpenAI batch input for chat or embeddings.
func ParseBatchJSONL(raw []byte, endpoint string) (ParsedInput, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint != EndpointChatCompletions && endpoint != EndpointEmbeddings {
		return ParsedInput{}, derrors.New(derrors.CodeInvalid, "batch.ParseBatchJSONL", "unsupported endpoint").
			With("endpoint", endpoint)
	}
	lines := splitJSONL(raw)
	if len(lines) == 0 {
		return ParsedInput{}, derrors.New(derrors.CodeInvalid, "batch.ParseBatchJSONL", "empty input")
	}
	out := ParsedInput{
		Endpoint:    endpoint,
		BodyByID:    make(map[string]json.RawMessage, len(lines)),
		CustomOrder: make([]string, 0, len(lines)),
	}
	var model string
	for i, line := range lines {
		var req InputLine
		if err := json.Unmarshal(line, &req); err != nil {
			return ParsedInput{}, derrors.Wrap(err, derrors.CodeInvalid, "batch.ParseBatchJSONL", "parse line").
				With("line", strconv.Itoa(i+1))
		}
		req.CustomID = strings.TrimSpace(req.CustomID)
		if req.CustomID == "" {
			return ParsedInput{}, derrors.New(derrors.CodeInvalid, "batch.ParseBatchJSONL", "custom_id required").
				With("line", strconv.Itoa(i+1))
		}
		if strings.ToUpper(strings.TrimSpace(req.Method)) != "POST" {
			return ParsedInput{}, derrors.New(derrors.CodeInvalid, "batch.ParseBatchJSONL", "method must be POST").
				With("line", strconv.Itoa(i+1))
		}
		if strings.TrimSpace(req.URL) != endpoint {
			return ParsedInput{}, derrors.New(derrors.CodeInvalid, "batch.ParseBatchJSONL", "line url must match batch endpoint").
				With("line", strconv.Itoa(i+1))
		}
		lineModel, err := extractModelFromBody(req.Body)
		if err != nil {
			return ParsedInput{}, derrors.Wrap(err, derrors.CodeInvalid, "batch.ParseBatchJSONL", "model in body").
				With("line", strconv.Itoa(i+1))
		}
		if model == "" {
			model = lineModel
		} else if model != lineModel {
			return ParsedInput{}, derrors.New(derrors.CodeInvalid, "batch.ParseBatchJSONL", "all lines must use the same model")
		}
		if err := validateBodyForEndpoint(endpoint, req.Body); err != nil {
			return ParsedInput{}, derrors.Wrap(err, derrors.CodeInvalid, "batch.ParseBatchJSONL", "body").
				With("line", strconv.Itoa(i+1))
		}
		out.Lines = append(out.Lines, req)
		out.BodyByID[req.CustomID] = req.Body
		out.CustomOrder = append(out.CustomOrder, req.CustomID)
	}
	out.Model = model
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
