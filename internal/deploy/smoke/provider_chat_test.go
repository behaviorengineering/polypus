package smoke

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRunProviderChatEmptyContentIncludesBody(t *testing.T) {
	const body = `{"choices":[{"message":{"content":"","reasoning_details":[{"type":"reasoning.text","text":"thinking"}]}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runProviderChat(ctx, srv.URL, "gemini_free/gemma-4-26b-a4b-it", "minimal")
	if err == nil {
		t.Fatal("expected empty content error")
	}
	got := err.Error()
	if !strings.Contains(got, "empty content") {
		t.Fatalf("want empty content in error, got %q", got)
	}
	if !strings.Contains(got, "reasoning_details") {
		t.Fatalf("want truncated body in error, got %q", got)
	}
}
