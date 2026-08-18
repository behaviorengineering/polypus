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

func TestChatBodyIsStream(t *testing.T) {
	if chatBodyIsStream([]byte(`{"stream":true}`)) != true {
		t.Fatal("expected stream")
	}
	if chatBodyIsStream([]byte(`{"stream":false}`)) {
		t.Fatal("expected non-stream")
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

func TestDisableChatThinkingHonorsExplicitOn(t *testing.T) {
	in := []byte(`{"model":"@cf/google/gemma-4-26b-a4b-it","enable_thinking":true,"messages":[{"role":"user","content":"hi"}]}`)
	out, changed := disableChatThinkingInRequest(in)
	if changed {
		t.Fatalf("should not rewrite when thinking is on: %s", out)
	}
	if !chatBodyWantsThinking(in) {
		t.Fatal("expected thinking")
	}
}

func TestChatBodyWantsThinkingKwargs(t *testing.T) {
	in := []byte(`{"model":"x","chat_template_kwargs":{"enable_thinking":true}}`)
	if !chatBodyWantsThinking(in) {
		t.Fatal("expected kwargs thinking")
	}
	off := []byte(`{"model":"x","enable_thinking":false}`)
	if chatBodyWantsThinking(off) {
		t.Fatal("expected thinking off")
	}
}
