package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

func TestWriteBifrostChatSSEErrorBeforeStart(t *testing.T) {
	chunks := make(chan router.ChatStreamChunk)
	errCh := make(chan error, 1)
	close(chunks)
	errCh <- errors.New("upstream boom")
	close(errCh)

	rec := httptest.NewRecorder()
	err := writeBifrostChatSSE(context.Background(), rec, bifrostStream{chunks: chunks, errs: errCh})
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
	if err := writeBifrostChatSSE(context.Background(), rec, bifrostStream{chunks: chunks, errs: errCh}); err != nil {
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

// stallingRouter streams Bifrost chat chunks it is fed, then stalls until cancelled.
type stallingRouter struct {
	*fakeRouter
	first []router.ChatStreamChunk
}

func (s *stallingRouter) UsesBifrost(string) bool { return true }

func (s *stallingRouter) ChatCompletionStreamRaw(ctx context.Context, _ router.Call) (<-chan router.ChatStreamChunk, <-chan error, error) {
	chunks := make(chan router.ChatStreamChunk)
	errCh := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errCh)
		for _, c := range s.first {
			select {
			case chunks <- c:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
		errCh <- derrors.Wrap(ctx.Err(), derrors.CodeCanceled, "fake.stream", "context done")
	}()
	return chunks, errCh, nil
}

func stallingChatHandler(r *stallingRouter) chatHandler {
	return chatHandler{
		responder: testResponder,
		chat:      r,
		upstreams: upstream.NewBoard(),
		timeouts:  config.Timeouts{Min: time.Millisecond, Max: time.Second, StreamIdle: 100 * time.Millisecond},
	}
}

func TestBifrostStreamWithoutAFirstChunkHitsTheHop(t *testing.T) {
	h := stallingChatHandler(&stallingRouter{})
	start := time.Now()
	err := h.bifrostChatStream(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil),
		chatCall{target: backendTarget{id: "leaf"}, stream: true, hop: 100 * time.Millisecond})
	if derrors.CodeOf(err) != derrors.CodeTimeout || time.Since(start) > 3*time.Second {
		t.Fatalf("want a timeout at the hop: err=%v after %s", err, time.Since(start))
	}
}

func TestBifrostStreamThatStallsIsCutOffAndCountsAgainstTheBackend(t *testing.T) {
	h := stallingChatHandler(&stallingRouter{first: []router.ChatStreamChunk{
		{OpenAIJSON: []byte(`{"choices":[{"index":0,"delta":{"content":"hi"}}]}`)},
	}})
	rec := httptest.NewRecorder()
	start := time.Now()
	err := h.bifrostChatStream(rec, httptest.NewRequest(http.MethodPost, "/", nil),
		chatCall{target: backendTarget{id: "leaf"}, stream: true, hop: 5 * time.Second})
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("a stalled stream must be cut off at the idle limit: err=%v after %s", err, time.Since(start))
	}
	if derrors.CodeOf(err) != derrors.CodeTimeout || !errors.Is(err, upstream.ErrResponseWritten) {
		t.Fatalf("want a timeout after headers, got %v", err)
	}
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("the chunk before the stall must reach the client: %q", rec.Body.String())
	}
}
