package gateway

import (
	"strings"
	"testing"
)

func TestMergeReasoningIntoContent(t *testing.T) {
	in := []byte(`{"choices":[{"message":{"role":"assistant","content":null,"reasoning":"answer text"}}]}`)
	out, changed := mergeReasoningIntoContent(in)
	if !changed {
		t.Fatal("expected change")
	}
	if !strings.Contains(string(out), `"content":"answer text"`) {
		t.Fatalf("body=%s", out)
	}
}

func TestDisableChatThinkingInRequestGLM(t *testing.T) {
	in := []byte(`{"model":"@cf/zai-org/glm-4.7-flash","messages":[{"role":"user","content":"hi"}]}`)
	out, changed := disableChatThinkingInRequest(in)
	if !changed {
		t.Fatal("expected change")
	}
	if !strings.Contains(string(out), `"enable_thinking":false`) {
		t.Fatalf("body=%s", out)
	}
}
