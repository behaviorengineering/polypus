package gateway

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestOpenAIStreamSanitizerDropsPostStopChoice(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	stop := []byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	_, _, emit := splitSanitized(sanitizeOpenAIStreamChunk(stop, san, true))
	if !emit {
		t.Fatal("stop chunk must emit")
	}
	illegal := []byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":"late"}}]}`)
	_, action, emit := splitSanitized(sanitizeOpenAIStreamChunk(illegal, san, true))
	if emit || action != streamSSEDrop {
		t.Fatalf("expected drop, emit=%v action=%s", emit, action)
	}
}

func TestOpenAIStreamSanitizerRewritesUsageAfterStop(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	stop := []byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	sanitizeOpenAIStreamChunk(stop, san, true)
	usage := []byte(`{"id":"c1","choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	out, action, emit := splitSanitized(sanitizeOpenAIStreamChunk(usage, san, true))
	if !emit || action != streamSSERewriteUsage {
		t.Fatalf("emit=%v action=%s", emit, action)
	}
	if !strings.Contains(string(out), `"choices":[]`) {
		t.Fatalf("expected empty choices: %s", out)
	}
	if !strings.Contains(string(out), `"total_tokens":3`) {
		t.Fatalf("usage preserved: %s", out)
	}
}

func TestOpenAIStreamSanitizerStripsExtraFields(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	raw := []byte(`{"id":"c1","extra_fields":{"raw_response":{}},"choices":[{"index":0,"delta":{"content":"x"}}]}`)
	out, _, emit := splitSanitized(sanitizeOpenAIStreamChunk(raw, san, true))
	if !emit {
		t.Fatal("expected emit")
	}
	if strings.Contains(string(out), "extra_fields") {
		t.Fatalf("extra_fields leaked: %s", out)
	}
}

func TestWriteBifrostChatSSEDropsIllegalTrailingChunk(t *testing.T) {
	chunks := make(chan router.ChatStreamChunk, 3)
	errCh := make(chan error, 1)
	chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":"Hi"}}]}`)}
	chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)}
	chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":"oops"}}]}`)}
	close(chunks)
	close(errCh)

	rec := httptest.NewRecorder()
	if err := writeBifrostChatSSE(context.Background(), rec, bifrostStream{chunks: chunks, errs: errCh}); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"content":"oops"`) {
		t.Fatalf("illegal chunk forwarded: %s", body)
	}
}

func TestChatStreamSanitizerDropsIllegalChunk(t *testing.T) {
	upstream := strings.Join([]string{
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n",
		"\n",
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
		"\n",
		"data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late\"}}]}\n",
		"\n",
		"data: [DONE]\n",
		"\n",
	}, "")
	out := gatewaySSE(t, upstream)
	if strings.Contains(out, `"content":"late"`) {
		t.Fatalf("illegal proxy chunk forwarded: %s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("missing stop: %s", out)
	}
}

func TestChatStreamLegalPassthrough(t *testing.T) {
	legal := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"
	if got, want := gatewaySSE(t, legal), legal+"data: [DONE]\n\n"; got != want {
		t.Fatalf("expected the legal frame unchanged, then DONE\ngot: %q\nwant: %q", got, want)
	}
}

func TestChatStreamEmptyEndsWithDone(t *testing.T) {
	if got := gatewaySSE(t, "data: [DONE]\n\n"); got != "data: [DONE]\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeFailOpenOnMalformedJSON(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	bad := []byte(`not-json`)
	out, action, emit := splitSanitized(sanitizeOpenAIStreamChunk(bad, san, false))
	if !emit || action != streamSSEForward || !bytes.Equal(out, bad) {
		t.Fatalf("fail open: emit=%v action=%s out=%q", emit, action, out)
	}
}

func TestSanitizePerIndexFinish(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	sanitizeOpenAIStreamChunk([]byte(`{"choices":[{"index":0,"finish_reason":"stop","delta":{}}]}`), san, false)
	// index 1 still allowed.
	out, _, emit := splitSanitized(sanitizeOpenAIStreamChunk([]byte(`{"choices":[{"index":1,"delta":{"content":"ok"}}]}`), san, false))
	if !emit || !strings.Contains(string(out), `"content":"ok"`) {
		t.Fatalf("index 1 should forward: %q", out)
	}
}
