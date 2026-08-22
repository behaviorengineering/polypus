package gateway

import (
	"net/http"
	"net/http/httptest"
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

func TestProxyChatCompletionsStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			t.Fatalf("accept: %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	t.Cleanup(upstream.Close)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"test","messages":[],"stream":true}`,
	))
	body := []byte(`{"model":"test","messages":[],"stream":true}`)
	err := proxyChatCompletions(rec, req, upstream.URL, body, upstream.Client(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("body: %q", rec.Body.String())
	}
}
