package router

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

// TestGeminiGemmaLive exercises Bifrost native Gemini with the same UseRawRequestBody
// path as ChatCompletionRaw. Skip when GEMINI_API_KEY is unset.
//
// Stage 4 note: if this test fails with a parse/400 from Google while non-raw Input
// works, disable BifrostContextKeyUseRawRequestBody for gemini backends in client.go.
func TestGeminiGemmaLive(t *testing.T) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	t.Setenv("GEMINI_API_KEY", key)
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	const model = "gemma-4-26b-a4b-it"
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"gemini_studio": {
				ID:        "gemini_studio",
				Remote:    true,
				Extension: config.ExtensionGemini,
				Auth:      config.BackendAuth{BearerEnv: "GEMINI_API_KEY"},
				Capabilities: []config.Capability{
					config.CapChat,
				},
			},
		},
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	body := []byte(`{"messages":[{"role":"user","content":"Reply with exactly: ok"}],"model":"` + model + `"}`)
	ctx := context.Background()
	timeout := 120 * time.Second

	raw, err := client.ChatCompletionRaw(ctx, "gemini_studio", model, body, timeout)
	if err != nil {
		t.Fatalf("ChatCompletionRaw: %v", err)
	}
	content := assistantContentFromChatJSON(raw)
	if strings.TrimSpace(content) == "" {
		t.Fatalf("empty assistant content: %s", string(raw))
	}

	streamBody := []byte(`{"messages":[{"role":"user","content":"Reply with exactly: ok"}],"model":"` + model + `","stream":true}`)
	chunks, errCh, err := client.ChatCompletionStreamRaw(ctx, "gemini_studio", model, streamBody, timeout)
	if err != nil {
		t.Fatalf("ChatCompletionStreamRaw: %v", err)
	}
	var streamed string
	for chunk := range chunks {
		streamed += deltaContentFromChunk(chunk)
	}
	if err := drainStreamErr(errCh); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings.TrimSpace(streamed) == "" {
		t.Fatal("empty streamed assistant content")
	}
}

func assistantContentFromChatJSON(raw []byte) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ""
	}
	if len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].Message.Content
}

func deltaContentFromChunk(chunk []byte) string {
	var c struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(chunk, &c); err != nil {
		return ""
	}
	if len(c.Choices) == 0 {
		return ""
	}
	return c.Choices[0].Delta.Content
}

func drainStreamErr(errCh <-chan error) error {
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}
