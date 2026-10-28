package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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

func newChatProxyClient(limit time.Duration) *http.Client {
	if limit <= 0 {
		limit = config.DefaultTimeouts().Max
	}
	return &http.Client{
		Timeout:   limit,
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

// chatCompletionsURL joins base (with or without trailing /v1 or /v1/openai) to the OpenAI chat path.
func chatCompletionsURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if isOpenAICompatAPIRoot(base) {
		return base + "/chat/completions"
	}
	return base + "/v1/chat/completions"
}

const (
	headerRouterSelectedModel     = "x-model-router-selected-model"
	headerSwitchyardSelectedModel = "x-switchyard-selected-model"
)

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

// chatDial is one chat completion sent to an OpenAI-shaped backend or Switchyard.
type chatDial struct {
	backendURL string
	client     *http.Client
	// hop bounds a non-stream call end to end, and a stream until its headers arrive.
	hop time.Duration
	// streamIdle is the longest gap allowed between stream chunks.
	streamIdle  time.Duration
	backendAuth string
	// stream is whether the body asks for SSE.
	stream bool
	// recordRouterSelectedModel tags the span with the model a router picked.
	recordRouterSelectedModel bool
	log                       *slog.Logger
	// dumpDir receives dropped stream chunks; empty turns the dumps off.
	dumpDir string
}

func proxyChat(w http.ResponseWriter, r *http.Request, body []byte, d chatDial) error {
	if d.log == nil {
		return derrors.New(derrors.CodeInternal, "gateway.proxyChat", "chat dial needs a logger")
	}
	target := chatCompletionsURL(d.backendURL)
	observability.RecordProxyIO(r.Context(), target, d.stream, 0, -1)
	hop := hopBudget(d.hop, config.DefaultTimeouts().Chat)
	// Non-stream: the hop bounds the whole call. Stream: the hop bounds the wait for
	// headers, then the idle limit bounds each gap between chunks.
	var ctx context.Context
	var cancel context.CancelFunc
	if d.stream {
		ctx, cancel = context.WithCancel(r.Context())
	} else {
		ctx, cancel = context.WithTimeout(r.Context(), hop)
	}
	defer cancel()
	req, err := d.newRequest(ctx, r, target, body, hop)
	if err != nil {
		return err
	}
	resp, err := d.send(ctx, req, hop, cancel)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeUnavailable, "gateway.proxyChat", "post").
			With("target", target)
	}
	defer func() { _ = resp.Body.Close() }()
	if d.stream {
		resp.Body = newIdleTimeoutBody(resp.Body, d.streamIdle, cancel)
		return d.writeStreamResponse(w, r, resp, target)
	}
	return d.writeResponse(w, r, resp, target)
}

// newRequest builds the backend POST. The backend's own credential wins; without one
// the caller's Authorization header is forwarded.
func (d chatDial) newRequest(ctx context.Context, r *http.Request, target string, body []byte, hop time.Duration) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyChat", "request")
	}
	req.Header.Set("Content-Type", "application/json")
	if d.stream {
		req.Header.Set("Accept", "text/event-stream, application/json")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	if d.backendAuth != "" {
		req.Header.Set("Authorization", d.backendAuth)
	} else if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set(config.TimeoutHeader, hop.String())
	return req, nil
}

// send dials once. A stream must get its headers within hop (cancel ends it
// otherwise); a non-stream call is bounded by ctx.
func (d chatDial) send(ctx context.Context, req *http.Request, hop time.Duration, cancel context.CancelFunc) (*http.Response, error) {
	if d.stream {
		firstByte := time.AfterFunc(hop, cancel)
		resp, err := streamSafeClient(d.client).Do(req) //nolint:gosec // G704: target is the operator-configured backend URL
		if !firstByte.Stop() && err != nil {
			err = derrors.Wrap(err, derrors.CodeTimeout, "gateway.proxyChat", "no response headers within the hop budget").
				With("hop", hop.String())
		}
		return resp, err
	}
	client := d.client
	if client == nil {
		client = newChatProxyClient(0)
	}
	// Sent once: retries belong to the caller and the fallback router, which get the
	// first 429 or 5xx with its Retry-After.
	return outbound.DoOnce(ctx, outbound.DepGateway, client, req)
}

// writeResponse relays a non-stream answer: a rate limit becomes an OpenAI 429 body,
// and a success has reasoning merged into content (and the router's pick recorded).
func (d chatDial) writeResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, target string) error {
	raw, err := readUpstream(resp.Body, chatMaxBody, "gateway.proxyChat")
	if err != nil {
		observability.RecordProxyIO(r.Context(), target, false, resp.StatusCode, -1)
		return err
	}
	observability.RecordProxyIO(r.Context(), target, false, resp.StatusCode, len(raw))
	status := resp.StatusCode
	if rewritten, ok := openaiRateLimitBody(resp.StatusCode, resp.Header, raw); ok {
		raw = rewritten
		status = http.StatusTooManyRequests
	} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if fixed, changed := mergeReasoningIntoContent(raw); changed {
			raw = fixed
			setIntervention(w, r.Context(), observability.InterventionMergeReasoningIntoContent)
		}
		if choiceHasBothReasoningSpellings(raw) {
			setIntervention(w, r.Context(), observability.InterventionReasoningAlias)
			if stripped, ok := stripReasoningAliasJSON(raw); ok {
				raw = stripped
			}
		}
		if d.recordRouterSelectedModel {
			observability.RecordDownstreamModel(r.Context(), extractRouterSelectedModel(resp.Header, raw))
		}
	}
	copyChatResponseHeaders(w, resp.Header)
	w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
	w.WriteHeader(status)
	if _, err := w.Write(raw); err != nil {
		return afterHeaders(r.Context(), err, true, "gateway.proxyChat", "write response")
	}
	return upstream.StatusFailure(status)
}

func (d chatDial) writeStreamResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, target string) error {
	streaming, log := d.stream, d.log
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, err := readUpstream(resp.Body, chatMaxBody, "gateway.proxyChat")
		if err != nil {
			observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, -1)
			return err
		}
		status := resp.StatusCode
		if rewritten, ok := openaiRateLimitBody(resp.StatusCode, resp.Header, raw); ok {
			raw = rewritten
			status = http.StatusTooManyRequests
		}
		copyChatResponseHeaders(w, resp.Header)
		w.Header().Set("Content-Length", strconv.Itoa(len(raw)))
		w.WriteHeader(status)
		_, err = w.Write(raw)
		observability.RecordProxyIO(r.Context(), target, streaming, status, len(raw))
		if err != nil {
			return afterHeaders(r.Context(), err, true, "gateway.proxyChat", "write stream")
		}
		return upstream.StatusFailure(status)
	}
	copyChatResponseHeaders(w, resp.Header)
	ctx := withStreamChunkCounter(r.Context(), log, d.dumpDir)
	n, err := copyOpenAIChatStreamWithOpts(ctx, w, io.LimitReader(resp.Body, chatMaxBody), false, &streamCopyOpts{
		responseWriter:   w,
		statusCode:       resp.StatusCode,
		delayWriteHeader: true,
	})
	observability.RecordProxyIO(r.Context(), target, streaming, resp.StatusCode, int(n))
	if err != nil {
		return afterHeaders(r.Context(), err, false, "gateway.proxyChat", "copy stream")
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

// applyChatThinkingRoot maps inbound OpenAI reasoning to the backend's own fields in
// root, and reports whether it changed anything.
func applyChatThinkingRoot(root map[string]any, extension string, model string) bool {
	wants := chatMapWantsThinking(root)
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		model = strings.ToLower(strings.TrimSpace(fmt.Sprint(root[keyModel])))
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
	return changed
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

// reasoningEffortMinimal is the lowest OpenAI reasoning effort; Polypus treats it as
// thinking off.
const reasoningEffortMinimal = "minimal"

func applyGeminiThinkingEmit(root map[string]any, model string, wants bool) bool {
	changed := false
	switch {
	case strings.Contains(model, "gemma-4"):
		effort := reasoningEffortMinimal
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
		effort := reasoningEffortMinimal
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

// reasoningObject returns root["reasoning"], creating it when absent. ok is false
// when the caller sent a non-object reasoning value, which is left untouched.
func reasoningObject(root map[string]any) (map[string]any, bool) {
	raw, present := root["reasoning"]
	if !present || raw == nil {
		reasoning := map[string]any{}
		root["reasoning"] = reasoning
		return reasoning, true
	}
	reasoning, ok := raw.(map[string]any)
	return reasoning, ok
}

func setNestedReasoningEffort(root map[string]any, effort string) bool {
	reasoning, ok := reasoningObject(root)
	if !ok {
		return false
	}
	if cur, ok := reasoning["effort"]; ok && strings.EqualFold(strings.TrimSpace(fmt.Sprint(cur)), effort) {
		return false
	}
	reasoning["effort"] = effort
	return true
}

func setNestedReasoningMaxTokens(root map[string]any, tokens int) bool {
	reasoning, ok := reasoningObject(root)
	if !ok {
		return false
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

func jsonStringField(raw json.RawMessage) string {
	if isJSONNull(raw) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
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
	case "", "<nil>", "nil", "none", "off", "false", "0", reasoningEffortMinimal:
		return true
	default:
		return false
	}
}

func chatMapWantsThinking(root map[string]any) bool {
	return !chatReasoningEffortIsOff(chatMapReasoningEffort(root))
}
