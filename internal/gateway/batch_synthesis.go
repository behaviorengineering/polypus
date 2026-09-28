package gateway

import (
	"encoding/json"
	"strings"
	"time"

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

func buildBatchOutputLines(endpoint, publicModel string, items []cloudflare.BatchResponseItem) (successJSONL, errorJSONL []byte) {
	var okLines, errLines []string
	now := time.Now().UTC().Unix()
	for _, item := range items {
		customID := strings.TrimSpace(item.ExternalReference)
		lineID := "batch_req_" + customID
		if !item.Success {
			errObj := batchOutputLine{
				ID:       lineID,
				CustomID: customID,
				Error: &batchLineError{
					Code:    "batch_failed",
					Message: strings.TrimSpace(item.ErrorMessage),
				},
			}
			if errObj.Error.Message == "" {
				errObj.Error.Message = "workers ai batch line failed"
			}
			raw, _ := json.Marshal(errObj)
			errLines = append(errLines, string(raw))
			continue
		}
		body, err := synthesizeOpenAIBody(endpoint, publicModel, now, item.Result)
		if err != nil {
			errObj := batchOutputLine{
				ID:       lineID,
				CustomID: customID,
				Error: &batchLineError{
					Code:    "synthesis_failed",
					Message: err.Error(),
				},
			}
			raw, _ := json.Marshal(errObj)
			errLines = append(errLines, string(raw))
			continue
		}
		okObj := batchOutputLine{
			ID:       lineID,
			CustomID: customID,
			Response: &batchLineResp{
				StatusCode: 200,
				Body:       body,
			},
		}
		raw, _ := json.Marshal(okObj)
		okLines = append(okLines, string(raw))
	}
	if len(okLines) > 0 {
		successJSONL = []byte(strings.Join(okLines, "\n") + "\n")
	}
	if len(errLines) > 0 {
		errorJSONL = []byte(strings.Join(errLines, "\n") + "\n")
	}
	return successJSONL, errorJSONL
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
			"model":   publicModel,
		}
		_ = json.Unmarshal(result, &wrap)
		wrap["model"] = publicModel
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
		"model":   publicModel,
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
			"model":  publicModel,
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
				"model":  publicModel,
				"data":   vec.Data,
			}
			return json.Marshal(wrap)
		}
	}
	body := map[string]interface{}{
		"object": "list",
		"model":  publicModel,
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
