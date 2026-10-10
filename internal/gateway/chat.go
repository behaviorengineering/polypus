package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const (
	chatMaxBody        = 32 << 20
	errBackendNotFound = "polypus: backend not found"
)

func newChatProxyClient(max time.Duration) *http.Client {
	if max <= 0 {
		max = config.DefaultTimeouts().Max
	}
	return &http.Client{
		Timeout:   max,
		Transport: observability.WrapTransport(http.DefaultTransport),
	}
}

// newStreamProxyClient returns a client for SSE: no Client.Timeout (that covers the
// entire body read). Bound connect/TTFB via request context; cancel on client disconnect.
func newStreamProxyClient() *http.Client {
	return &http.Client{
		Transport: observability.WrapTransport(http.DefaultTransport),
	}
}

func streamSafeClient(client *http.Client) *http.Client {
	if client == nil {
		return newStreamProxyClient()
	}
	if client.Timeout == 0 {
		return client
	}
	return &http.Client{Transport: client.Transport}
}

func extractChatModel(body []byte) (string, error) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", derrors.Wrap(err, derrors.CodeInvalid, "gateway.extractChatModel", "invalid json")
	}
	model := strings.TrimSpace(payload.Model)
	if model == "" {
		return "", derrors.New(derrors.CodeInvalid, "gateway.extractChatModel", "model required")
	}
	return model, nil
}

func rewriteChatModel(body []byte, model string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "gateway.rewriteChatModel", "invalid json")
	}
	payload["model"] = model
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.rewriteChatModel", "marshal")
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

// chatCompletionsURL joins base (with or without trailing /v1) to the OpenAI chat path.
func chatCompletionsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	base = strings.TrimSuffix(base, "/v1")
	return base + "/v1/chat/completions"
}

const (
	headerRouterSelectedModel     = "x-model-router-selected-model"
	headerSwitchyardSelectedModel = "x-switchyard-selected-model"
)

func proxyChatCompletions(w http.ResponseWriter, r *http.Request, backendURL string, body []byte, client *http.Client, hopTimeout time.Duration, backendAuth string) error {
	model, _ := extractChatModel(body)
	return proxyChatCompletionsOpts(w, r, backendURL, body, client, hopTimeout, backendAuth, true, "", model, false)
}

// extractRouterSelectedModel reads Switchyard routing metadata from response headers or JSON body.
func extractRouterSelectedModel(hdr http.Header, body []byte) string {
	for _, key := range []string{headerRouterSelectedModel, headerSwitchyardSelectedModel} {
		if v := strings.TrimSpace(hdr.Get(key)); v != "" {
			return v
		}
	}
	var root struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	return strings.TrimSpace(root.Model)
}

func proxyChatCompletionsOpts(w http.ResponseWriter, r *http.Request, backendURL string, body []byte, client *http.Client, hopTimeout time.Duration, backendAuth string, patchThinking bool, thinkingExtension string, thinkingModel string, recordRouterSelectedModel bool) error {
	if patchThinking {
		if patched, ok := applyChatThinking(body, thinkingExtension, thinkingModel); ok {
			body = patched
		}
	}
	target := chatCompletionsURL(backendURL)
	streaming := chatBodyIsStream(body)
	observability.RecordProxyIO(r.Context(), target, streaming, 0, -1)
	ctx := r.Context()
	// Non-stream: hop wall-clock via context. Stream: only client cancel (r.Context);
	// Client.Timeout must also stay unset for streams (see streamSafeClient).
	if hopTimeout > 0 && !streaming {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hopTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyChat", "request")
	}
	req.Header.Set("Content-Type", "application/json")
	if streaming {
		req.Header.Set("Accept", "text/event-stream, application/json")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if backendAuth != "" {
		req.Header.Set("Authorization", backendAuth)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if hopTimeout > 0 {
		req.Header.Set(config.TimeoutHeader, hopTimeout.String())
	}

	if streaming {
		client = streamSafeClient(client)
	} else if client == nil {
		client = newChatProxyClient(0)
	}
	var resp *http.Response
	if streaming {
		resp, err = client.Do(req)
	} else {
		resp, err = outbound.Do(ctx, outbound.DepGateway, client, req)
	}
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxyChat", "post").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()

	if streaming {
		return writeChatStreamResponse(w, r, resp, target, streaming)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, chatMaxBody))
	if err != nil {
		observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, -1)
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxyChat", "read response")
	}
	observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, len(raw))
	status := resp.StatusCode
	if rewritten, ok := openaiRateLimitBody(resp.StatusCode, resp.Header, raw); ok {
		raw = rewritten
		status = http.StatusTooManyRequests
	} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if fixed, changed := mergeReasoningIntoContent(raw); changed {
			raw = fixed
		}
		if recordRouterSelectedModel {
			observability.RecordDownstreamModel(r.Context(), extractRouterSelectedModel(resp.Header, raw))
		}
	}

	copyChatResponseHeaders(w, resp.Header)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(raw)))
	w.WriteHeader(status)
	_, err = w.Write(raw)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyChat", "write response")
	}
	return upstream.StatusFailure(status)
}

func writeChatStreamResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, target string, streaming bool) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, chatMaxBody))
		if err != nil {
			observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, -1)
			return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxyChat", "read stream")
		}
		status := resp.StatusCode
		if rewritten, ok := openaiRateLimitBody(resp.StatusCode, resp.Header, raw); ok {
			raw = rewritten
			status = http.StatusTooManyRequests
		}
		copyChatResponseHeaders(w, resp.Header)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(raw)))
		w.WriteHeader(status)
		_, err = w.Write(raw)
		observability.RecordProxyIO(r.Context(), target, streaming, status, len(raw))
		if err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyChat", "write stream")
		}
		return upstream.StatusFailure(status)
	}
	copyChatResponseHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)
	n, err := io.Copy(w, io.LimitReader(resp.Body, chatMaxBody))
	observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, int(n))
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyChat", "write stream")
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return upstream.StatusFailure(resp.StatusCode)
}

func copyChatResponseHeaders(w http.ResponseWriter, header http.Header) {
	for k, vals := range header {
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
}

// applyChatThinking maps inbound OpenAI reasoning to backend-specific outbound fields.
func applyChatThinking(body []byte, extension string, model string) ([]byte, bool) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false
	}
	wants := chatMapWantsThinking(root)
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		model = strings.ToLower(strings.TrimSpace(fmt.Sprint(root["model"])))
	}
	ext := strings.ToLower(strings.TrimSpace(extension))
	var changed bool
	switch ext {
	case config.ExtensionGemini:
		changed = stripCloudflareThinkingFields(root)
		changed = applyGeminiThinkingEmit(root, model, wants) || changed
	case config.ExtensionCloudflare:
		changed = applyCloudflareThinkingEmit(root, model, wants)
	default:
		changed = stripCloudflareThinkingFields(root)
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

func stripCloudflareThinkingFields(root map[string]any) bool {
	changed := false
	if _, ok := root["chat_template_kwargs"]; ok {
		delete(root, "chat_template_kwargs")
		changed = true
	}
	if _, ok := root["enable_thinking"]; ok {
		delete(root, "enable_thinking")
		changed = true
	}
	return changed
}

func applyGeminiThinkingEmit(root map[string]any, model string, wants bool) bool {
	changed := false
	switch {
	case strings.Contains(model, "gemma-4"):
		effort := "minimal"
		if wants {
			effort = "high"
		}
		changed = setNestedReasoningEffort(root, effort) || changed
		changed = deleteNestedReasoningField(root, "max_tokens") || changed
	case strings.Contains(model, "gemini-2.5"):
		tokens := 0
		if wants {
			tokens = -1
		}
		changed = setNestedReasoningMaxTokens(root, tokens) || changed
		changed = deleteNestedReasoningField(root, "effort") || changed
	case strings.Contains(model, "gemini-3"):
		effort := "minimal"
		if wants {
			effort = strings.ToLower(strings.TrimSpace(chatMapReasoningEffort(root)))
			if effort == "" || chatReasoningEffortIsOff(effort) {
				effort = "high"
			}
		}
		changed = setNestedReasoningEffort(root, effort) || changed
	default:
		if wants {
			return false
		}
	}
	return changed
}

func applyCloudflareThinkingEmit(root map[string]any, model string, wants bool) bool {
	if strings.Contains(model, "deepseek") {
		if wants {
			return false
		}
		if _, ok := root["reasoning_effort"]; ok {
			return false
		}
		root["reasoning_effort"] = "none"
		return true
	}
	if !strings.Contains(model, "gemma") && !strings.Contains(model, "glm") && !strings.Contains(model, "zai-org") {
		return false
	}
	changed := false
	kwargs, ok := root["chat_template_kwargs"].(map[string]any)
	if !ok {
		kwargs = map[string]any{}
		root["chat_template_kwargs"] = kwargs
		changed = true
	}
	if v, exists := kwargs["enable_thinking"]; !exists || v != wants {
		kwargs["enable_thinking"] = wants
		changed = true
	}
	if wants {
		if v, exists := root["enable_thinking"]; !exists || v != true {
			root["enable_thinking"] = true
			changed = true
		}
	} else if v, exists := root["enable_thinking"]; !exists || v != false {
		root["enable_thinking"] = false
		changed = true
	}
	return changed
}

func setNestedReasoningEffort(root map[string]any, effort string) bool {
	reasoning, _ := root["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
		root["reasoning"] = reasoning
	}
	if cur, ok := reasoning["effort"]; ok && strings.EqualFold(strings.TrimSpace(fmt.Sprint(cur)), effort) {
		return false
	}
	reasoning["effort"] = effort
	return true
}

func setNestedReasoningMaxTokens(root map[string]any, tokens int) bool {
	reasoning, _ := root["reasoning"].(map[string]any)
	if reasoning == nil {
		reasoning = map[string]any{}
		root["reasoning"] = reasoning
	}
	cur, ok := reasoning["max_tokens"]
	if ok {
		switch n := cur.(type) {
		case float64:
			if int(n) == tokens {
				return false
			}
		case int:
			if n == tokens {
				return false
			}
		}
	}
	reasoning["max_tokens"] = tokens
	return true
}

func deleteNestedReasoningField(root map[string]any, field string) bool {
	reasoning, ok := root["reasoning"].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := reasoning[field]; !exists {
		return false
	}
	delete(reasoning, field)
	if len(reasoning) == 0 {
		delete(root, "reasoning")
	}
	return true
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

// ensureStreamChoiceFinishReason adds "finish_reason":null on each stream choice
// that omits the key. Bifrost marshals nil finish reasons with omitempty, which
// breaks OpenAI-compat clients (llm-go) that require the field on every chunk.
func ensureStreamChoiceFinishReason(body []byte) ([]byte, bool) {
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
		if _, has := choices[i]["finish_reason"]; has {
			continue
		}
		choices[i]["finish_reason"] = json.RawMessage("null")
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

func chatBodyWantsThinking(body []byte) bool {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return false
	}
	return chatMapWantsThinking(root)
}

func chatMapReasoningEffort(root map[string]any) string {
	if reasoning, ok := root["reasoning"].(map[string]any); ok {
		if v, ok := reasoning["effort"]; ok {
			s := strings.TrimSpace(fmt.Sprint(v))
			if s != "" && s != "<nil>" && s != "nil" {
				return s
			}
		}
	}
	if v, ok := root["reasoning_effort"]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func chatReasoningEffortIsOff(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "<nil>", "nil", "none", "off", "false", "0", "minimal":
		return true
	default:
		return false
	}
}

func chatMapWantsThinking(root map[string]any) bool {
	return !chatReasoningEffortIsOff(chatMapReasoningEffort(root))
}
