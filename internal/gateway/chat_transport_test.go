package gateway

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestWriteBifrostChatSSEErrorBeforeStart(t *testing.T) {
	chunks := make(chan router.ChatStreamChunk)
	errCh := make(chan error, 1)
	close(chunks)
	errCh <- errors.New("upstream boom")
	close(errCh)

	rec := httptest.NewRecorder()
	err := writeBifrostChatSSE(context.Background(), rec, chunks, errCh)
	if err == nil || !strings.Contains(err.Error(), "upstream boom") {
		t.Fatalf("err=%v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("should not write SSE body on setup error: %q", rec.Body.String())
	}
}

func TestWriteBifrostChatSSEInjectsNullFinishReason(t *testing.T) {
	chunks := make(chan router.ChatStreamChunk, 2)
	errCh := make(chan error, 1)
	chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"c1","choices":[{"index":0,"delta":{"content":"Hi"}}]}`)}
	chunks <- router.ChatStreamChunk{OpenAIJSON: []byte(`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)}
	close(chunks)
	close(errCh)

	rec := httptest.NewRecorder()
	if err := writeBifrostChatSSE(context.Background(), rec, chunks, errCh); err != nil {
		t.Fatalf("write: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"finish_reason":null`) {
		t.Fatalf("mid chunk missing finish_reason null: %s", body)
	}
	if !strings.Contains(body, `"finish_reason":"stop"`) {
		t.Fatalf("terminal finish_reason lost: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("missing DONE: %s", body)
	}
}
