package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
)

const (
	chatMaxBody        = 32 << 20
	msgBackendNotFound = "polypus: backend not found"
)

const (
	headerRouterSelectedModel     = "x-model-router-selected-model"
	headerSwitchyardSelectedModel = "x-switchyard-selected-model"
)

// extractRouterSelectedModel reads Switchyard routing metadata from response headers or JSON body.
func extractRouterSelectedModel(hdr http.Header, body []byte) string {
	for _, key := range selectedModelHeaders {
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

// selectedModel records the model a composed router (Switchyard) picked on the
// request span, once: from the router's response headers, else from the first
// response body that names a model. A nil *selectedModel records nothing.
type selectedModel struct {
	ctx      context.Context
	recorded bool
}

// selectedModelHeaders are the router response headers that reach the caller. The
// gateway forwards no other Switchyard header.
var selectedModelHeaders = []string{headerRouterSelectedModel, headerSwitchyardSelectedModel}

// fromHeader copies the router's selected-model headers to the caller (before the
// status is written) and records the pick they name.
func (s *selectedModel) fromHeader(w http.ResponseWriter, header http.Header) {
	if s == nil {
		return
	}
	for _, key := range selectedModelHeaders {
		if v := strings.TrimSpace(header.Get(key)); v != "" {
			w.Header().Set(key, v)
		}
	}
	s.record(extractRouterSelectedModel(header, nil))
}

// fromBody records the model a response body (or stream chunk) names, unless a
// pick is already recorded.
func (s *selectedModel) fromBody(body []byte) {
	if s == nil || s.recorded {
		return
	}
	s.record(extractRouterSelectedModel(nil, body))
}

func (s *selectedModel) record(model string) {
	if s.recorded || model == "" {
		return
	}
	observability.RecordDownstreamModel(s.ctx, model)
	s.recorded = true
}

// applyChatThinkingRoot maps inbound OpenAI reasoning to the backend's own fields in
// root.
func applyChatThinkingRoot(root map[string]any, extension string, model string) {
	wants := chatMapWantsThinking(root)
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		bodyModel, _ := root[keyModel].(string)
		model = strings.ToLower(strings.TrimSpace(bodyModel))
	}
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case config.ExtensionGemini:
		stripCloudflareThinkingFields(root)
		applyGeminiThinkingEmit(root, model, wants)
	case config.ExtensionCloudflare:
		applyCloudflareThinkingEmit(root, model, wants)
	default:
		stripCloudflareThinkingFields(root)
	}
}

func stripCloudflareThinkingFields(root map[string]any) {
	delete(root, keyChatTemplateKwargs)
	delete(root, keyEnableThinking)
}

// reasoningEffortMinimal is the lowest OpenAI reasoning effort; Polypus treats it as
// thinking off.
const reasoningEffortMinimal = "minimal"

func applyGeminiThinkingEmit(root map[string]any, model string, wants bool) {
	switch {
	case strings.Contains(model, "gemma-4"):
		effort := reasoningEffortMinimal
		if wants {
			effort = "high"
		}
		setNestedReasoningEffort(root, effort)
		deleteNestedReasoningField(root, keyMaxTokens)
	case strings.Contains(model, "gemini-2.5"):
		tokens := 0
		if wants {
			tokens = -1
		}
		setNestedReasoningMaxTokens(root, tokens)
		deleteNestedReasoningField(root, keyEffort)
	case strings.Contains(model, "gemini-3"):
		effort := reasoningEffortMinimal
		if wants {
			effort = strings.ToLower(strings.TrimSpace(chatMapReasoningEffort(root)))
			if effort == "" || chatReasoningEffortIsOff(effort) {
				effort = "high"
			}
		}
		setNestedReasoningEffort(root, effort)
	}
}

func applyCloudflareThinkingEmit(root map[string]any, model string, wants bool) {
	if strings.Contains(model, "deepseek") {
		if wants {
			return
		}
		if _, ok := root[keyReasoningEffort]; !ok {
			root[keyReasoningEffort] = "none"
		}
		return
	}
	if !strings.Contains(model, "gemma") && !strings.Contains(model, "glm") && !strings.Contains(model, "zai-org") {
		return
	}
	kwargs, ok := root[keyChatTemplateKwargs].(map[string]any)
	if !ok {
		kwargs = map[string]any{}
		root[keyChatTemplateKwargs] = kwargs
	}
	kwargs[keyEnableThinking] = wants
	root[keyEnableThinking] = wants
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

// setNestedReasoningEffort sets reasoning.effort, keeping the caller's spelling of
// an equal effort.
func setNestedReasoningEffort(root map[string]any, effort string) {
	reasoning, ok := reasoningObject(root)
	if !ok {
		return
	}
	if cur, ok := reasoning[keyEffort].(string); ok && strings.EqualFold(strings.TrimSpace(cur), effort) {
		return
	}
	reasoning[keyEffort] = effort
}

// setNestedReasoningMaxTokens sets reasoning.max_tokens, keeping the caller's own
// encoding of an equal number.
func setNestedReasoningMaxTokens(root map[string]any, tokens int) {
	reasoning, ok := reasoningObject(root)
	if !ok {
		return
	}
	if cur, ok := reasoning[keyMaxTokens]; ok && jsonIntEquals(cur, tokens) {
		return
	}
	reasoning[keyMaxTokens] = tokens
}

func deleteNestedReasoningField(root map[string]any, field string) {
	reasoning, ok := root[keyReasoning].(map[string]any)
	if !ok {
		return
	}
	if _, exists := reasoning[field]; !exists {
		return
	}
	delete(reasoning, field)
	if len(reasoning) == 0 {
		delete(root, keyReasoning)
	}
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
