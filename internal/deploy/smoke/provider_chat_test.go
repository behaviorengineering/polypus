package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/progress"
)

// chatServer answers /health and /v1/chat/completions with content, and records
// each chat request's body and Authorization header.
func chatServer(t *testing.T, content string, bodies, auths *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/v1/chat/completions":
			body, _ := readBody(r)
			*bodies = append(*bodies, body)
			*auths = append(*auths, r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
			_, _ = w.Write(reply)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRunChatSendsReasoningEffortThroughTheSharedProbe checks a provider chat probe
// goes through internal/smoke with the target's reasoning effort, the probe's token
// budget and the client key, and prints its result lines.
func TestRunChatSendsReasoningEffortThroughTheSharedProbe(t *testing.T) {
	var bodies, auths []string
	srv := chatServer(t, "ok", &bodies, &auths)
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ep := endpoint{url: srv.URL, key: "pk_test", out: progress.New(&out)}
	if err := runChat(ctx, ep, chatProbe{model: "gemini_free/gemma-4-26b-a4b-it", reasoningEffort: "minimal"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 {
		t.Fatalf("chat requests %d, want 1", len(bodies))
	}
	var req struct {
		Model     string  `json:"model"`
		MaxTokens int     `json:"max_tokens"`
		Temp      float64 `json:"temperature"`
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "gemini_free/gemma-4-26b-a4b-it" || req.MaxTokens != 64 || req.Temp != 0.2 {
		t.Fatalf("request %+v, want the model with 64 tokens at 0.2", req)
	}
	if req.Reasoning == nil || req.Reasoning.Effort != "minimal" {
		t.Fatalf("reasoning %+v, want effort minimal", req.Reasoning)
	}
	if auths[0] != "Bearer pk_test" {
		t.Fatalf("authorization %q, want Bearer pk_test", auths[0])
	}
	if !strings.Contains(out.String(), "channel=chat probe=content_nonempty model=gemini_free/gemma-4-26b-a4b-it") ||
		!strings.Contains(out.String(), "status=pass") {
		t.Fatalf("printed %q, want the probe's pass line", out.String())
	}
}

func TestRunChatWithoutReasoningEffortSendsNone(t *testing.T) {
	var bodies, auths []string
	srv := chatServer(t, "ok", &bodies, &auths)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runChat(ctx, endpoint{url: srv.URL, out: progress.New(nil)}, chatProbe{model: "work/acme"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || strings.Contains(bodies[0], "reasoning") {
		t.Fatalf("bodies %q, want one without reasoning", bodies)
	}
}

func TestRunChatEmptyContentFails(t *testing.T) {
	var bodies, auths []string
	srv := chatServer(t, "", &bodies, &auths)
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runChat(ctx, endpoint{url: srv.URL, out: progress.New(&out)}, chatProbe{model: "work/acme", reasoningEffort: "minimal"})
	if err == nil || !strings.Contains(err.Error(), "empty content") {
		t.Fatalf("err = %v, want empty content", err)
	}
	if !strings.Contains(out.String(), "status=fail") {
		t.Fatalf("printed %q, want a fail line", out.String())
	}
}
