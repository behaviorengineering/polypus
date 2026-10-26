package gateway

import (
	"encoding/json"
	"strings"

	"github.com/behaviorengineering/polypus/internal/gateway/router"

	"github.com/behaviorengineering/polypus/internal/batch"
	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
)

type batchOutputLine struct {
	ID       string          `json:"id"`
	CustomID string          `json:"custom_id"`
	Response *batchLineResp  `json:"response"`
	Error    *batchLineError `json:"error"`
}

type batchLineResp struct {
	StatusCode int             `json:"status_code"`
	Body       json.RawMessage `json:"body"`
}

type batchLineError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// cloudflareBatchLines turns Workers AI batch items into batch lines, synthesising the
// OpenAI response body for each success. buildBatchLineOutput writes the JSONL.
func cloudflareBatchLines(endpoint, publicModel string, createdUnix int64, items []cloudflare.BatchResponseItem) []router.BatchLineResult {
	out := make([]router.BatchLineResult, 0, len(items))
	for _, item := range items {
		line := router.BatchLineResult{CustomID: item.ExternalReference}
		if !item.Success {
			line.ErrCode = "batch_failed"
			line.ErrMessage = firstNonEmpty(strings.TrimSpace(item.ErrorMessage), "workers ai batch line failed")
			out = append(out, line)
			continue
		}
		body, err := synthesizeOpenAIBody(endpoint, publicModel, createdUnix, item.Result)
		if err != nil {
			line.ErrCode = "synthesis_failed"
			line.ErrMessage = err.Error()
		} else {
			line.Body = body
		}
		out = append(out, line)
	}
	return out
}

func synthesizeOpenAIBody(endpoint, publicModel string, created int64, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 {
		return nil, errBatchSynthesis("empty result")
	}
	switch endpoint {
	case batch.EndpointChatCompletions:
		return synthesizeChatBody(publicModel, created, result)
	case batch.EndpointEmbeddings:
		return synthesizeEmbedBody(publicModel, created, result)
	default:
		return nil, errBatchSynthesis("unsupported endpoint")
	}
}

func synthesizeChatBody(publicModel string, created int64, result json.RawMessage) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(result, &probe); err != nil {
		return nil, err
	}
	if raw, ok := probe["choices"]; ok && len(raw) > 0 {
		wrap := map[string]interface{}{
			"id":      "chatcmpl-batch",
			"object":  "chat.completion",
			"created": created,
			keyModel:  publicModel,
		}
		_ = json.Unmarshal(result, &wrap)
		wrap[keyModel] = publicModel
		wrap["created"] = created
		return json.Marshal(wrap)
	}
	content := ""
	var text struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(result, &text); err == nil && strings.TrimSpace(text.Response) != "" {
		content = text.Response
	}
	body := map[string]interface{}{
		"id":      "chatcmpl-batch",
		"object":  "chat.completion",
		"created": created,
		keyModel:  publicModel,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int{
			"prompt_tokens":     0,
			"completion_tokens": 0,
			"total_tokens":      0,
		},
	}
	return json.Marshal(body)
}

func synthesizeEmbedBody(publicModel string, created int64, result json.RawMessage) (json.RawMessage, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(result, &probe); err != nil {
		return nil, err
	}
	if raw, ok := probe["data"]; ok && len(raw) > 0 {
		wrap := map[string]interface{}{
			"object": "list",
			keyModel: publicModel,
			"data":   json.RawMessage(raw),
		}
		return json.Marshal(wrap)
	}
	var vec struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(result, &vec); err == nil {
		if len(vec.Data) > 0 {
			wrap := map[string]interface{}{
				"object": "list",
				keyModel: publicModel,
				"data":   vec.Data,
			}
			return json.Marshal(wrap)
		}
	}
	body := map[string]interface{}{
		"object": "list",
		keyModel: publicModel,
		"data": []map[string]interface{}{
			{
				"object":    "embedding",
				"index":     0,
				"embedding": result,
			},
		},
	}
	return json.Marshal(body)
}

type batchSynthError string

func (e batchSynthError) Error() string { return string(e) }

func errBatchSynthesis(msg string) error { return batchSynthError(msg) }
