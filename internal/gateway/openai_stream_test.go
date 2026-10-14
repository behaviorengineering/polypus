package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestOpenAIStreamSanitizerDropsPostStopChoice(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	stop := []byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	_, _, emit := sanitizeOpenAIStreamChunk(stop, san, true)
	if !emit {
		t.Fatal("stop chunk must emit")
	}
	illegal := []byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":"late"}}]}`)
	_, action, emit := sanitizeOpenAIStreamChunk(illegal, san, true)
	if emit || action != StreamSSEDrop {
		t.Fatalf("expected drop, emit=%v action=%s", emit, action)
	}
}

func TestOpenAIStreamSanitizerRewritesUsageAfterStop(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	stop := []byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	sanitizeOpenAIStreamChunk(stop, san, true)
	usage := []byte(`{"id":"c1","choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	out, action, emit := sanitizeOpenAIStreamChunk(usage, san, true)
	if !emit || action != StreamSSERewriteUsage {
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
	out, _, emit := sanitizeOpenAIStreamChunk(raw, san, true)
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
	if err := writeBifrostChatSSE(context.Background(), rec, chunks, errCh); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, `"content":"oops"`) {
		t.Fatalf("illegal chunk forwarded: %s", body)
	}
}

func TestProxyChatStreamSanitizerDropsIllegalChunk(t *testing.T) {
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
	var buf bytes.Buffer
	n, err := copyOpenAIChatStream(&buf, strings.NewReader(upstream), false)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if n == 0 {
		t.Fatal("expected bytes written")
	}
	out := buf.String()
	if strings.Contains(out, `"content":"late"`) {
		t.Fatalf("illegal proxy chunk forwarded: %s", out)
	}
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Fatalf("missing stop: %s", out)
	}
}

func TestProxyChatStreamLegalPassthrough(t *testing.T) {
	legal := "data: {\"id\":\"c1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"},\"finish_reason\":null}]}\n\n"
	var buf bytes.Buffer
	_, err := copyOpenAIChatStream(&buf, strings.NewReader(legal), false)
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != legal {
		t.Fatalf("expected unchanged legal frame\ngot: %q\nwant: %q", buf.String(), legal)
	}
}

func TestCopyOpenAIChatStreamPreservesDone(t *testing.T) {
	src := "data: [DONE]\n\n"
	var buf bytes.Buffer
	_, err := copyOpenAIChatStream(&buf, strings.NewReader(src), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "data: [DONE]") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestSanitizeFailOpenOnMalformedJSON(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	bad := []byte(`not-json`)
	out, action, emit := sanitizeOpenAIStreamChunk(bad, san, false)
	if !emit || action != StreamSSEForward || !bytes.Equal(out, bad) {
		t.Fatalf("fail open: emit=%v action=%s out=%q", emit, action, out)
	}
}

func TestSanitizePerIndexFinish(t *testing.T) {
	san := newOpenAIStreamSanitizer()
	sanitizeOpenAIStreamChunk([]byte(`{"choices":[{"index":0,"finish_reason":"stop","delta":{}}]}`), san, false)
	// index 1 still allowed
	out, _, emit := sanitizeOpenAIStreamChunk([]byte(`{"choices":[{"index":1,"delta":{"content":"ok"}}]}`), san, false)
	if !emit || !strings.Contains(string(out), `"content":"ok"`) {
		t.Fatalf("index 1 should forward: %q", out)
	}
}

// Ensure io.EOF path in copyOpenAIChatStream.
func TestCopyOpenAIChatStreamEOF(t *testing.T) {
	src := "data: {\"choices\":[{\"index\":0,\"delta\":{}}]}\n"
	var buf bytes.Buffer
	_, err := copyOpenAIChatStream(&buf, io.MultiReader(strings.NewReader(src)), false)
	if err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected flush on EOF")
	}
}
