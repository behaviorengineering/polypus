package router

import (
	"errors"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestChatParamsFromOpenAIBodyMaxTokens(t *testing.T) {
	t.Parallel()
	params, err := chatParamsFromOpenAIBody([]byte(`{"messages":[],"max_tokens":42}`))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || params.MaxCompletionTokens == nil || *params.MaxCompletionTokens != 42 {
		t.Fatalf("params: %+v", params)
	}
}

func TestChatParamsFromOpenAIBodyMaxCompletionTokensWins(t *testing.T) {
	t.Parallel()
	params, err := chatParamsFromOpenAIBody([]byte(`{"messages":[],"max_tokens":42,"max_completion_tokens":7}`))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || params.MaxCompletionTokens == nil || *params.MaxCompletionTokens != 7 {
		t.Fatalf("params: %+v", params)
	}
}

func TestChatParamsFromOpenAIBodyEmpty(t *testing.T) {
	t.Parallel()
	params, err := chatParamsFromOpenAIBody([]byte(`{"model":"m","messages":[],"stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if params != nil {
		t.Fatalf("params: %+v", params)
	}
}

func TestChatParamsFromOpenAIBodyTools(t *testing.T) {
	t.Parallel()
	body := `{"messages":[],
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
		"tool_choice":"auto","parallel_tool_calls":false}`
	params, err := chatParamsFromOpenAIBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || len(params.Tools) != 1 || params.Tools[0].Function == nil || params.Tools[0].Function.Name != "get_weather" {
		t.Fatalf("tools: %+v", params)
	}
	if params.ToolChoice == nil || params.ToolChoice.ChatToolChoiceStr == nil || *params.ToolChoice.ChatToolChoiceStr != "auto" {
		t.Fatalf("tool_choice: %+v", params.ToolChoice)
	}
	if params.ParallelToolCalls == nil || *params.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls: %+v", params.ParallelToolCalls)
	}
}

func TestChatParamsFromOpenAIBodySamplingAndFormat(t *testing.T) {
	t.Parallel()
	body := `{"messages":[],"temperature":0.2,"top_p":0.9,"stop":["END"],
		"response_format":{"type":"json_object"},"stream_options":{"include_usage":true},
		"cache_control":{"type":"ephemeral"}}`
	params, err := chatParamsFromOpenAIBody([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || params.Temperature == nil || *params.Temperature != 0.2 || params.TopP == nil || *params.TopP != 0.9 {
		t.Fatalf("sampling: %+v", params)
	}
	if len(params.Stop) != 1 || params.Stop[0] != "END" {
		t.Fatalf("stop: %+v", params.Stop)
	}
	if params.ResponseFormat == nil || params.StreamOptions == nil {
		t.Fatalf("response_format or stream_options missing: %+v", params)
	}
	if params.CacheControl == nil || params.CacheControl.Type != schemas.CacheControlTypeEphemeral {
		t.Fatalf("cache_control: %+v", params.CacheControl)
	}
}

func TestChatParamsFromOpenAIBodyReasoning(t *testing.T) {
	t.Parallel()
	params, err := chatParamsFromOpenAIBody([]byte(`{"messages":[],"reasoning":{"effort":"high","max_tokens":2048}}`))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || params.Reasoning == nil || params.Reasoning.Effort == nil || *params.Reasoning.Effort != "high" {
		t.Fatalf("reasoning: %+v", params)
	}
	if params.Reasoning.MaxTokens == nil || *params.Reasoning.MaxTokens != 2048 {
		t.Fatalf("reasoning max_tokens: %+v", params.Reasoning)
	}
}

func TestChatParamsFromOpenAIBodyReasoningShorthand(t *testing.T) {
	t.Parallel()
	params, err := chatParamsFromOpenAIBody([]byte(`{"messages":[],"reasoning_effort":"low"}`))
	if err != nil {
		t.Fatal(err)
	}
	if params == nil || params.Reasoning == nil || params.Reasoning.Effort == nil || *params.Reasoning.Effort != "low" {
		t.Fatalf("reasoning: %+v", params)
	}
}

func TestChatParamsFromOpenAIBodyConflictingReasoning(t *testing.T) {
	t.Parallel()
	_, err := chatParamsFromOpenAIBody([]byte(`{"messages":[],"reasoning_effort":"low","reasoning":{"effort":"high"}}`))
	var de *derrors.Error
	if !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
		t.Fatalf("err: %v", err)
	}
}

func TestBifrostChatRequestNonRawCarriesTools(t *testing.T) {
	t.Parallel()
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":"required"}`)
	req, err := bifrostChatRequest("gemini_paid", "gemini-3-flash", nil, body, false)
	if err != nil {
		t.Fatal(err)
	}
	if req.RawRequestBody != nil {
		t.Fatal("non-raw request must not carry the raw body")
	}
	if req.Params == nil || len(req.Params.Tools) != 1 || req.Params.ToolChoice == nil {
		t.Fatalf("params: %+v", req.Params)
	}
}

func TestBifrostChatRequestRawSkipsParams(t *testing.T) {
	t.Parallel()
	body := []byte(`{"messages":[],"tools":[{"type":"function","function":{"name":"f"}}]}`)
	req, err := bifrostChatRequest("openrouter", "m", nil, body, true)
	if err != nil {
		t.Fatal(err)
	}
	if req.Params != nil || string(req.RawRequestBody) != string(body) {
		t.Fatalf("raw request: params=%+v raw=%s", req.Params, req.RawRequestBody)
	}
}

func TestChatParamsFromOpenAIBodyInvalidJSON(t *testing.T) {
	t.Parallel()
	_, err := chatParamsFromOpenAIBody([]byte(`{`))
	if err == nil {
		t.Fatal("expected error")
	}
	var de *derrors.Error
	if !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
		t.Fatalf("err: %v", err)
	}
}
