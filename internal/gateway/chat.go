package gateway

import (
	"bytes"
	"context"
	"encoding/json"
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
	msgBackendNotFound = "polypus: backend not found"
)

// proxyTransport is how the gateway sends its own HTTP hops to OpenAI-shaped
// backends. Every field is required.
type proxyTransport struct {
	// client sends non-stream calls; its Timeout is the outer Max budget.
	client *http.Client
	// streamClient sends streams. It has no Client.Timeout, which would cover the
	// whole body read: the hop and idle timers bound a stream instead.
	streamClient *http.Client
	// policies is the gateway's own outbound breaker set.
	policies *outbound.Policies
}

func newProxyTransport(limit time.Duration, policies *outbound.Policies) proxyTransport {
	if policies == nil {
		panic("gateway: newProxyTransport needs outbound policies")
	}
	if limit <= 0 {
		limit = config.DefaultTimeouts().Max
	}
	rt := observability.WrapTransport(http.DefaultTransport)
	return proxyTransport{
		client:       &http.Client{Timeout: limit, Transport: rt},
		streamClient: &http.Client{Transport: rt},
		policies:     policies,
	}
}

func (t proxyTransport) ready() bool {
	return t.client != nil && t.streamClient != nil && t.policies != nil
}

// newBackendPOST builds a JSON POST to target with the backend credential, or the
// caller's Authorization when the backend has none. accept is the Accept header.
func newBackendPOST(ctx context.Context, in *http.Request, target, backendAuth, accept string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	if backendAuth == "" {
		backendAuth = in.Header.Get("Authorization")
	}
	if backendAuth != "" {
		req.Header.Set("Authorization", backendAuth)
	}
	return req, nil
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
	transport  proxyTransport
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
	// chunkDumps is where dropped stream chunks are dumped.
	chunkDumps chunkDumpSink
}

func proxyChat(w http.ResponseWriter, r *http.Request, body []byte, d chatDial) error {
	if d.log == nil || !d.transport.ready() {
		return derrors.New(derrors.CodeInternal, "gateway.proxyChat", "chat dial needs a logger and a transport")
	}
	target := chatCompletionsURL(d.backendURL)
	observability.RecordProxyIO(r.Context(), observability.ProxyIO{Target: target, Streaming: d.stream, BodyBytes: -1})
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
	accept := "application/json"
	if d.stream {
		accept = "text/event-stream, application/json"
	}
	req, err := newBackendPOST(ctx, r, target, d.backendAuth, accept, body)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "gateway.proxyChat", "request")
	}
	req.Header.Set(config.TimeoutHeader, hop.String())
	return req, nil
}

// send dials once. A stream must get its headers within hop (cancel ends it
// otherwise); a non-stream call is bounded by ctx.
func (d chatDial) send(ctx context.Context, req *http.Request, hop time.Duration, cancel context.CancelFunc) (*http.Response, error) {
	if d.stream {
		// Streams carry no ctx deadline: the first-byte timer and the idle body bound
		// them, so the dial goes through the breaker without a deadline check.
		firstByte := time.AfterFunc(hop, cancel)
		resp, err := d.transport.policies.DoStreamOnce(ctx, outbound.DepGateway, d.transport.streamClient, req)
		if !firstByte.Stop() && err != nil {
			err = derrors.Wrap(err, derrors.CodeTimeout, "gateway.proxyChat", "no response headers within the hop budget").
				With("hop", hop.String())
		}
		return resp, err
	}
	// Sent once: retries belong to the caller and the fallback router, which get the
	// first 429 or 5xx with its Retry-After.
	return d.transport.policies.DoOnce(ctx, outbound.DepGateway, d.transport.client, req)
}

// writeResponse relays a non-stream answer: a rate limit becomes an OpenAI 429 body,
// and a success has reasoning merged into content (and the router's pick recorded).
func (d chatDial) writeResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, target string) error {
	raw, err := readUpstream(resp.Body, chatMaxBody, "gateway.proxyChat")
	if err != nil {
		observability.RecordProxyIO(r.Context(), observability.ProxyIO{Target: target, StatusCode: resp.StatusCode, BodyBytes: -1})
		return err
	}
	observability.RecordProxyIO(r.Context(), observability.ProxyIO{Target: target, StatusCode: resp.StatusCode, BodyBytes: len(raw)})
	status := resp.StatusCode
	if rewritten, ok := openaiRateLimitBody(resp.StatusCode, resp.Header, raw); ok {
		raw = rewritten
		status = http.StatusTooManyRequests
	} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		raw = normalizeChatCompletion(r.Context(), w, raw)
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, err := readUpstream(resp.Body, chatMaxBody, "gateway.proxyChat")
		if err != nil {
			observability.RecordProxyIO(r.Context(), observability.ProxyIO{Target: target, Streaming: true, StatusCode: resp.StatusCode, BodyBytes: -1})
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
		observability.RecordProxyIO(r.Context(), observability.ProxyIO{Target: target, Streaming: true, StatusCode: status, BodyBytes: len(raw)})
		if err != nil {
			return afterHeaders(r.Context(), err, true, "gateway.proxyChat", "write stream")
		}
		return upstream.StatusFailure(status)
	}
	copyChatResponseHeaders(w, resp.Header)
	ctx := withStreamChunkCounter(r.Context(), d.chunkDumps)
	n, err := copyOpenAIChatStreamWithOpts(ctx, w, io.LimitReader(resp.Body, chatMaxBody), false, &streamCopyOpts{
		responseWriter:   w,
		statusCode:       resp.StatusCode,
		delayWriteHeader: true,
	})
	observability.RecordProxyIO(r.Context(), observability.ProxyIO{Target: target, Streaming: true, StatusCode: resp.StatusCode, BodyBytes: int(n)})
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
		bodyModel, _ := root[keyModel].(string)
		model = strings.ToLower(strings.TrimSpace(bodyModel))
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
	if _, ok := root[keyChatTemplateKwargs]; ok {
		delete(root, keyChatTemplateKwargs)
		changed = true
	}
	if _, ok := root[keyEnableThinking]; ok {
		delete(root, keyEnableThinking)
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
		changed = deleteNestedReasoningField(root, keyMaxTokens) || changed
	case strings.Contains(model, "gemini-2.5"):
		tokens := 0
		if wants {
			tokens = -1
		}
		changed = setNestedReasoningMaxTokens(root, tokens) || changed
		changed = deleteNestedReasoningField(root, keyEffort) || changed
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
		if _, ok := root[keyReasoningEffort]; ok {
			return false
		}
		root[keyReasoningEffort] = "none"
		return true
	}
	if !strings.Contains(model, "gemma") && !strings.Contains(model, "glm") && !strings.Contains(model, "zai-org") {
		return false
	}
	changed := false
	kwargs, ok := root[keyChatTemplateKwargs].(map[string]any)
	if !ok {
		kwargs = map[string]any{}
		root[keyChatTemplateKwargs] = kwargs
		changed = true
	}
	if v, exists := kwargs[keyEnableThinking]; !exists || v != wants {
		kwargs[keyEnableThinking] = wants
		changed = true
	}
	if v, exists := root[keyEnableThinking]; !exists || v != wants {
		root[keyEnableThinking] = wants
		changed = true
	}
	return changed
}

// reasoningObject returns root["reasoning"], creating it when absent. ok is false
// when the caller sent a non-object reasoning value, which is left untouched.
func reasoningObject(root map[string]any) (map[string]any, bool) {
	raw, present := root[keyReasoning]
	if !present || raw == nil {
		reasoning := map[string]any{}
		root[keyReasoning] = reasoning
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
	if cur, ok := reasoning[keyEffort].(string); ok && strings.EqualFold(strings.TrimSpace(cur), effort) {
		return false
	}
	reasoning[keyEffort] = effort
	return true
}

func setNestedReasoningMaxTokens(root map[string]any, tokens int) bool {
	reasoning, ok := reasoningObject(root)
	if !ok {
		return false
	}
	if cur, ok := reasoning[keyMaxTokens]; ok && jsonIntEquals(cur, tokens) {
		return false
	}
	reasoning[keyMaxTokens] = tokens
	return true
}

func deleteNestedReasoningField(root map[string]any, field string) bool {
	reasoning, ok := root[keyReasoning].(map[string]any)
	if !ok {
		return false
	}
	if _, exists := reasoning[field]; !exists {
		return false
	}
	delete(reasoning, field)
	if len(reasoning) == 0 {
		delete(root, keyReasoning)
	}
	return true
}

// jsonIntEquals reports whether a decoded JSON number equals n. A request decoded
// with UseNumber holds json.Number; a value the gateway set holds int.
func jsonIntEquals(v any, n int) bool {
	switch x := v.(type) {
	case json.Number:
		got, err := x.Int64()
		return err == nil && got == int64(n)
	case int:
		return x == n
	case float64:
		return x == float64(n)
	default:
		return false
	}
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

// chatMapReasoningEffort reads the requested effort: reasoning.effort, else the flat
// reasoning_effort. A value that is not a string is no effort.
func chatMapReasoningEffort(root map[string]any) string {
	if reasoning, ok := root[keyReasoning].(map[string]any); ok {
		if s, ok := reasoning[keyEffort].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	if s, ok := root[keyReasoningEffort].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func chatReasoningEffortIsOff(effort string) bool {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none", "off", "false", "0", reasoningEffortMinimal:
		return true
	default:
		return false
	}
}

func chatMapWantsThinking(root map[string]any) bool {
	return !chatReasoningEffortIsOff(chatMapReasoningEffort(root))
}
