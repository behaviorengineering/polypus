package gateway

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteBifrostChatSSEErrorBeforeStart(t *testing.T) {
	chunks := make(chan []byte)
	errCh := make(chan error, 1)
	close(chunks)
	errCh <- errors.New("upstream boom")
	close(errCh)

	rec := httptest.NewRecorder()
	err := writeBifrostChatSSE(rec, chunks, errCh)
	if err == nil || !strings.Contains(err.Error(), "upstream boom") {
		t.Fatalf("err=%v", err)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("should not write SSE body on setup error: %q", rec.Body.String())
	}
}
