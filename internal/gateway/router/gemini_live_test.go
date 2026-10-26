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
		streamed += deltaContentFromChunk(chunk.OpenAIJSON)
	}
	if err := drainStreamErr(errCh); err != nil {
		t.Fatalf("stream: %v", err)
	}
	if strings.TrimSpace(streamed) == "" {
		t.Fatal("empty streamed assistant content")
	}
}

// geminiLiveKeyEnv names the env var that holds the Gemini key for live tests.
const geminiLiveKeyEnv = "GEMINI_API_KEY"

// TestGeminiToolCallLive checks that tools and tool_choice reach native Gemini through
// the non-raw path. Skip when GEMINI_API_KEY is unset; GEMINI_TOOL_MODEL overrides the model.
func TestGeminiToolCallLive(t *testing.T) {
	key := strings.TrimSpace(os.Getenv(geminiLiveKeyEnv))
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	t.Setenv(geminiLiveKeyEnv, key)
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	model := strings.TrimSpace(os.Getenv("GEMINI_TOOL_MODEL"))
	if model == "" {
		model = "gemini-2.5-flash"
	}
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"gemini_paid": {
				ID:           "gemini_paid",
				Remote:       true,
				Extension:    config.ExtensionGemini,
				Auth:         config.BackendAuth{BearerEnv: geminiLiveKeyEnv},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	body := []byte(`{"model":"` + model + `",
		"messages":[{"role":"user","content":"What is the weather in Sydney? Use the tool."}],
		"tools":[{"type":"function","function":{"name":"get_weather","description":"Current weather for a city",
			"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}],
		"tool_choice":"required"}`)
	raw, err := client.ChatCompletionRaw(context.Background(), "gemini_paid", model, body, 120*time.Second)
	if err != nil {
		t.Fatalf("ChatCompletionRaw: %v", err)
	}
	var resp struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode: %v: %s", err, raw)
	}
	if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 ||
		resp.Choices[0].Message.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("expected a get_weather tool call: %s", raw)
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
