package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/observability"
)

const chatProxyTimeout = 900 * time.Second
const chatMaxBody = 32 << 20

var chatProxyClient = &http.Client{
	Timeout:   chatProxyTimeout,
	Transport: observability.WrapTransport(http.DefaultTransport),
}

func extractChatModel(body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("invalid json: %w", err)
	}
	model, _ := payload["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return "", fmt.Errorf("model required")
	}
	return model, nil
}

func rewriteChatModel(body []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	payload["model"] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return out, nil
}

func chatBodyIsStream(body []byte) bool {
	var payload struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Stream
}

func chatBodyHasVision(body []byte) bool {
	var payload struct {
		Messages []struct {
			Content any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	for _, msg := range payload.Messages {
		parts, ok := msg.Content.([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(m["type"])), "image_url") {
				return true
			}
		}
	}
	return false
}

func proxyChatCompletions(w http.ResponseWriter, r *http.Request, backendURL string, body []byte) error {
	if patched, ok := disableChatThinkingInRequest(body); ok {
		body = patched
	}
	target := strings.TrimRight(strings.TrimSpace(backendURL), "/") + "/v1/chat/completions"
	streaming := chatBodyIsStream(body)
	observability.RecordProxyIO(r.Context(), target, streaming, 0, -1)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := chatProxyClient.Do(req)
	if err != nil {
		return fmt.Errorf("post %s: %w", target, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, chatMaxBody))
	if err != nil {
		observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, -1)
		return fmt.Errorf("read response: %w", err)
	}
	observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, len(raw))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if fixed, changed := mergeReasoningIntoContent(raw); changed {
			raw = fixed
		}
	}

	for k, vals := range resp.Header {
		if len(vals) == 0 {
			continue
		}
		switch strings.ToLower(k) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
			"te", "trailers", "transfer-encoding", "upgrade", "content-length":
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(raw)))
	w.WriteHeader(resp.StatusCode)
	_, err = w.Write(raw)
	if err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}

// disableChatThinkingInRequest asks CF/OpenAI-compat backends not to burn tokens on reasoning.
func disableChatThinkingInRequest(body []byte) ([]byte, bool) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false
	}
	model := strings.ToLower(strings.TrimSpace(fmt.Sprint(root["model"])))
	needs := strings.Contains(model, "gemma") ||
		strings.Contains(model, "glm") ||
		strings.Contains(model, "zai-org")
	if !needs {
		return body, false
	}
	changed := false
	if _, ok := root["chat_template_kwargs"]; !ok {
		root["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
		changed = true
	} else if kwargs, ok := root["chat_template_kwargs"].(map[string]any); ok {
		if v, exists := kwargs["enable_thinking"]; !exists || v != false {
			kwargs["enable_thinking"] = false
			changed = true
		}
	}
	if _, ok := root["reasoning_effort"]; !ok {
		root["reasoning_effort"] = nil
		changed = true
	}
	if _, ok := root["enable_thinking"]; !ok {
		root["enable_thinking"] = false
		changed = true
	}
	if !changed {
		return body, false
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return out, true
}

func mergeReasoningIntoContent(body []byte) ([]byte, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false
	}
	rawChoices, ok := root["choices"]
	if !ok {
		return body, false
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(rawChoices, &choices); err != nil || len(choices) == 0 {
		return body, false
	}
	changed := false
	for i := range choices {
		rawMsg, ok := choices[i]["message"]
		if !ok {
			continue
		}
		var msg map[string]json.RawMessage
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			continue
		}
		content := jsonStringField(msg["content"])
		reasoning := jsonStringField(msg["reasoning_content"])
		if reasoning == "" {
			reasoning = jsonStringField(msg["reasoning"])
		}
		if content != "" || reasoning == "" {
			continue
		}
		contentRaw, marshalErr := json.Marshal(reasoning)
		if marshalErr != nil {
			continue
		}
		msg["content"] = contentRaw
		updated, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		choices[i]["message"] = updated
		changed = true
	}
	if !changed {
		return body, false
	}
	updatedChoices, err := json.Marshal(choices)
	if err != nil {
		return body, false
	}
	root["choices"] = updatedChoices
	out, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return out, true
}

func jsonStringField(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
