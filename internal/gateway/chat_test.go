package gateway

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func chatJSONField(t *testing.T, body []byte, path ...string) any {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cur := root
	for i, key := range path {
		if i == len(path)-1 {
			return cur[key]
		}
		cur = cur[key].(map[string]any)
	}
	return nil
}

func TestApplyChatThinkingCloudflareGLMOff(t *testing.T) {
	in := []byte(`{"model":"@cf/zai-org/glm-4.7-flash","messages":[{"role":"user","content":"hi"}]}`)
	out, changed := applyChatThinking(in, config.ExtensionCloudflare, "glm-4.7-flash")
	if !changed {
		t.Fatal("expected change")
	}
	if chatJSONField(t, out, "chat_template_kwargs", "enable_thinking") != false {
		t.Fatal("expected false kwargs")
	}
}

func TestApplyChatThinkingGeminiGemma4DefaultOff(t *testing.T) {
	in := []byte(`{"model":"gemma-4-26b-a4b-it","messages":[{"role":"user","content":"hi"}]}`)
	out, changed := applyChatThinking(in, config.ExtensionGemini, "gemma-4-26b-a4b-it")
	if !changed {
		t.Fatal("expected change")
	}
	if chatJSONField(t, out, "reasoning", "effort") != "minimal" {
		t.Fatal("expected minimal")
	}
	if strings.Contains(string(out), "chat_template_kwargs") {
		t.Fatal("no kwargs")
	}
}

func TestApplyChatThinkingGeminiKwargsOnlyStripped(t *testing.T) {
	in := []byte(`{"model":"gemma-4-26b-a4b-it","chat_template_kwargs":{"enable_thinking":true},"messages":[{"role":"user","content":"hi"}]}`)
	out, _ := applyChatThinking(in, config.ExtensionGemini, "gemma-4-26b-a4b-it")
	if chatBodyWantsThinking(in) {
		t.Fatal("kwargs not opt-in")
	}
	if chatJSONField(t, out, "reasoning", "effort") != "minimal" {
		t.Fatal("expected minimal")
	}
}

func TestApplyChatThinkingGemini25OnOff(t *testing.T) {
	onOut, changed := applyChatThinking([]byte(`{"model":"gemini-2.5-flash","reasoning":{"effort":"high"},"messages":[]}`), config.ExtensionGemini, "gemini-2.5-flash")
	if !changed {
		t.Fatal("on")
	}
	if chatJSONField(t, onOut, "reasoning", "max_tokens") != float64(-1) {
		t.Fatal("max_tokens -1")
	}
	offOut, changed := applyChatThinking([]byte(`{"model":"gemini-2.5-flash","messages":[]}`), config.ExtensionGemini, "gemini-2.5-flash")
	if !changed || chatJSONField(t, offOut, "reasoning", "max_tokens") != float64(0) {
		t.Fatal("off")
	}
}

func TestChatBodyWantsThinkingOpenAIOnly(t *testing.T) {
	if !chatBodyWantsThinking([]byte(`{"reasoning":{"effort":"high"}}`)) {
		t.Fatal("high on")
	}
	if chatBodyWantsThinking([]byte(`{"chat_template_kwargs":{"enable_thinking":true}}`)) {
		t.Fatal("kwargs off")
	}
}

// applyChatThinking runs applyChatThinkingRoot over a JSON body and reports whether
// it changed the body.
func applyChatThinking(body []byte, extension, model string) ([]byte, bool) {
	var before, root map[string]any
	if json.Unmarshal(body, &before) != nil || json.Unmarshal(body, &root) != nil {
		return body, false
	}
	applyChatThinkingRoot(root, extension, model)
	if reflect.DeepEqual(before, root) {
		return body, false
	}
	out, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return out, true
}

// chatBodyWantsThinking reports whether a JSON body asks for reasoning.
func chatBodyWantsThinking(body []byte) bool {
	var root map[string]any
	return json.Unmarshal(body, &root) == nil && chatMapWantsThinking(root)
}

func TestReasoningMaxTokensMatchesDecodedNumber(t *testing.T) {
	root := map[string]any{keyReasoning: map[string]any{keyMaxTokens: json.Number("-1")}}
	setNestedReasoningMaxTokens(root, -1)
	if got := root[keyReasoning].(map[string]any)[keyMaxTokens]; got != json.Number("-1") {
		t.Fatalf("an equal json.Number must be kept as sent, got %#v", got)
	}
}

func TestReasoningEffortIgnoresNonStrings(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"reasoning":{"effort":7}}`, ""},
		{`{"reasoning_effort":1}`, ""},
		{`{"reasoning":{"effort":null},"reasoning_effort":"high"}`, "high"},
		{`{"reasoning":{"effort":" low "}}`, "low"},
	}
	for _, c := range cases {
		var root map[string]any
		if err := json.Unmarshal([]byte(c.body), &root); err != nil {
			t.Fatal(err)
		}
		if got := chatMapReasoningEffort(root); got != c.want {
			t.Fatalf("%s: effort %q want %q", c.body, got, c.want)
		}
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(`{"messages":[{"content":[{"type":7}]}]}`), &root); err != nil {
		t.Fatal(err)
	}
	if chatMapHasVision(root) {
		t.Fatal("a non-string part type is not vision")
	}
}
