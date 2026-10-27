package router

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestChatStreamChunkFromResponseEmitsReasoningContentAlias(t *testing.T) {
	reasoning := "hidden"
	content := "visible"
	chunk, err := chatStreamChunkFromResponse(&schemas.BifrostChatResponse{
		ID:     "chat",
		Object: "chat.completion.chunk",
		Model:  "openrouter/meta/muse-spark-1.3",
		Choices: []schemas.BifrostResponseChoice{{
			Index: 0,
			ChatStreamResponseChoice: &schemas.ChatStreamResponseChoice{
				Delta: &schemas.ChatStreamResponseChoiceDelta{
					Reasoning: &reasoning,
					Content:   &content,
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := deltaReasoningSpellings(t, chunk.OpenAIJSON)
	if !got.reasoning || !got.reasoningContent {
		t.Fatalf("Bifrost marshal must emit both spellings, got %+v body=%s", got, chunk.OpenAIJSON)
	}
	if got.reasoningText != reasoning || got.reasoningContentText != reasoning {
		t.Fatalf("alias values drifted: %+v", got)
	}
	if got.content != content {
		t.Fatalf("content = %q want %q", got.content, content)
	}
}

func TestChatCompletionMarshalEmitsReasoningContentAlias(t *testing.T) {
	reasoning := "hidden"
	content := "visible"
	raw, err := json.Marshal(&schemas.BifrostChatResponse{
		ID:     "chat",
		Object: "chat.completion",
		Model:  "openrouter/meta/muse-spark-1.3",
		Choices: []schemas.BifrostResponseChoice{{
			Index: 0,
			ChatNonStreamResponseChoice: &schemas.ChatNonStreamResponseChoice{
				Message: &schemas.ChatMessage{
					Role:    schemas.ChatMessageRoleAssistant,
					Content: &schemas.ChatMessageContent{ContentStr: &content},
					ChatAssistantMessage: &schemas.ChatAssistantMessage{
						Reasoning: &reasoning,
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := messageReasoningSpellings(t, raw)
	if !got.reasoning || !got.reasoningContent {
		t.Fatalf("non-stream marshal must emit both spellings, got %+v body=%s", got, raw)
	}
	if got.reasoningText != reasoning || got.reasoningContentText != reasoning {
		t.Fatalf("alias values drifted: %+v", got)
	}
}

type reasoningSpellings struct {
	reasoning, reasoningContent                  bool
	reasoningText, reasoningContentText, content string
}

func deltaReasoningSpellings(t *testing.T, raw []byte) reasoningSpellings {
	t.Helper()
	return choiceReasoningSpellings(t, raw, "delta")
}

func messageReasoningSpellings(t *testing.T, raw []byte) reasoningSpellings {
	t.Helper()
	return choiceReasoningSpellings(t, raw, "message")
}

func choiceReasoningSpellings(t *testing.T, raw []byte, field string) reasoningSpellings {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("root: %v", err)
	}
	var choices []map[string]json.RawMessage
	if err := json.Unmarshal(root["choices"], &choices); err != nil || len(choices) == 0 {
		t.Fatalf("choices: %v len=%d", err, len(choices))
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(choices[0][field], &obj); err != nil {
		t.Fatalf("%s: %v body=%s", field, err, raw)
	}
	var got reasoningSpellings
	if v, ok := obj["reasoning"]; ok && string(v) != "null" {
		got.reasoning = true
		if err := json.Unmarshal(v, &got.reasoningText); err != nil {
			t.Fatalf("reasoning: %v", err)
		}
	}
	if v, ok := obj["reasoning_content"]; ok && string(v) != "null" {
		got.reasoningContent = true
		if err := json.Unmarshal(v, &got.reasoningContentText); err != nil {
			t.Fatalf("reasoning_content: %v", err)
		}
	}
	if v, ok := obj["content"]; ok && string(v) != "null" {
		if err := json.Unmarshal(v, &got.content); err != nil {
			t.Fatalf("content: %v", err)
		}
	}
	return got
}
