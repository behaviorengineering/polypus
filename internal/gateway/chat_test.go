package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestStreamSafeClientClearsTimeout(t *testing.T) {
	s := streamSafeClient(newChatProxyClient(30 * time.Second))
	if s.Timeout != 0 {
		t.Fatal("timeout cleared")
	}
}

func TestProxyChatCompletionsStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(up.Close)
	rec := httptest.NewRecorder()
	body := []byte(`{"model":"test","messages":[],"stream":true}`)
	if err := proxyChatCompletions(rec, httptest.NewRequest(http.MethodPost, "/", nil), up.URL, body, up.Client(), 0, ""); err != nil {
		t.Fatal(err)
	}
}
