package router

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

func claudeBatchTestClient(t *testing.T, promptCache *bool) *Client {
	t.Helper()
	t.Setenv(anthropicTestKeyEnv, "secret-key-at-least-twenty-ch")
	b := anthropicBackend("")
	b.PromptCache = promptCache
	reg, err := NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{"claude_team": b},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Client{reg: reg}
}

func TestClaudeBatchParamsTranslatesOpenAIChat(t *testing.T) {
	c := claudeBatchTestClient(t, nil)
	bctx := c.bifrostContext(context.Background(), time.Minute, false)
	body := []byte(`{"model":"claude_team/claude-sonnet-5-5","stream":true,"max_tokens":256,
		"messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"hi"}]}`)
	params, err := c.claudeBatchParams(bctx, "claude_team", "claude-sonnet-5-5", body)
	if err != nil {
		t.Fatal(err)
	}
	if params["model"] != "claude-sonnet-5-5" {
		t.Fatalf("model: %v", params["model"])
	}
	if mt, ok := params["max_tokens"].(float64); !ok || mt != 256 {
		t.Fatalf("max_tokens: %v", params["max_tokens"])
	}
	if _, ok := params["stream"]; ok {
		t.Fatal("batch params must not stream")
	}
	if params["system"] == nil {
		t.Fatalf("system prompt should be hoisted to top-level system: %v", params)
	}
	msgs, _ := params["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages: %v", params["messages"])
	}
	cc, _ := params["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" {
		t.Fatalf("automatic prompt caching should apply to batch lines: %v", params["cache_control"])
	}
}

func TestClaudeBatchParamsRespectsPromptCacheOff(t *testing.T) {
	off := false
	c := claudeBatchTestClient(t, &off)
	bctx := c.bifrostContext(context.Background(), time.Minute, false)
	params, err := c.claudeBatchParams(bctx, "claude_team", "claude-sonnet-5-5", []byte(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := params["cache_control"]; ok {
		t.Fatalf("prompt_cache: false must add no marker: %v", params)
	}
}

func TestClaudeBatchParamsRejectsEmptyMessages(t *testing.T) {
	c := claudeBatchTestClient(t, nil)
	bctx := c.bifrostContext(context.Background(), time.Minute, false)
	if _, err := c.claudeBatchParams(bctx, "claude_team", "m", []byte(`{"messages":[]}`)); err == nil {
		t.Fatal("expected an error for a line without messages")
	}
}

func TestClaudeBatchLineSucceeded(t *testing.T) {
	bctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	item := schemas.BatchResultItem{
		CustomID: "job-1",
		Result: &schemas.BatchResultData{
			Type: "succeeded",
			Message: map[string]any{
				"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-sonnet-5-5",
				"content":     []any{map[string]any{"type": "text", "text": "hello"}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 3, "output_tokens": 1},
			},
		},
	}
	line := claudeBatchLine(bctx, item, "claude_team/claude-sonnet-5-5")
	if line.ErrCode != "" || len(line.Body) == 0 {
		t.Fatalf("line: %+v", line)
	}
	var chat struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(line.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Model != "claude_team/claude-sonnet-5-5" || len(chat.Choices) != 1 ||
		!strings.Contains(chat.Choices[0].Message.Content, "hello") || chat.Choices[0].FinishReason != "stop" {
		t.Fatalf("chat: %s", line.Body)
	}
}

func TestClaudeBatchLineErrored(t *testing.T) {
	bctx := schemas.NewBifrostContext(context.Background(), time.Now().Add(time.Minute))
	line := claudeBatchLine(bctx, schemas.BatchResultItem{
		CustomID: "job-2",
		Result:   &schemas.BatchResultData{Type: "errored"},
		Error:    &schemas.BatchResultError{Code: "invalid_request_error", Message: "max_tokens too large"},
	}, "claude_team/m")
	if line.ErrCode != "errored" || line.ErrMessage != "max_tokens too large" || len(line.Body) != 0 {
		t.Fatalf("line: %+v", line)
	}
	expired := claudeBatchLine(bctx, schemas.BatchResultItem{CustomID: "job-3", Result: &schemas.BatchResultData{Type: "expired"}}, "claude_team/m")
	if expired.ErrCode != "expired" || expired.ErrMessage == "" {
		t.Fatalf("expired: %+v", expired)
	}
}

func TestNewAccountAllowsClaudeBatchesWithCapability(t *testing.T) {
	t.Setenv(anthropicTestKeyEnv, "secret-key-at-least-twenty-ch")
	b := anthropicBackend("")
	b.Capabilities = []config.Capability{config.CapChat, config.CapBatch}
	pc, err := newAccount(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{"claude_team": b},
	}).GetConfigForProvider("claude_team")
	if err != nil {
		t.Fatal(err)
	}
	ar := pc.CustomProviderConfig.AllowedRequests
	if !ar.BatchCreate || !ar.BatchRetrieve || !ar.BatchResults || !ar.BatchCancel {
		t.Fatalf("want batch calls allowed: %+v", ar)
	}
}
