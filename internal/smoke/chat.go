package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/httpx"
)

func runChat(ctx context.Context, opts Options) ([]Result, error) {
	ping := timed(ChannelChat, "ping", "", func() (string, error) {
		if err := pingHealth(ctx, opts.BaseURL); err != nil {
			return "", err
		}
		return "health ok", nil
	})
	// One paid completion serves both the content and the selected-model probes.
	var selected string
	var chatErr error
	content := timed(ChannelChat, "content_nonempty", opts.ChatModel, func() (string, error) {
		var reply chatReply
		reply, chatErr = chatPing(ctx, opts, opts.ChatModel)
		if chatErr != nil {
			return "", chatErr
		}
		selected = reply.selectedModel
		d := strings.TrimSpace(reply.content)
		if d == "" {
			// The body shows why: a reasoning-only answer, a refusal, or a filter.
			return "", fmt.Errorf("empty content: %s", httpx.Truncate(string(reply.raw), 200))
		}
		return httpx.Truncate(d, 60), nil
	})
	out := []Result{ping, content}
	if opts.RequireSelectedModel || len(opts.AllowedSelectedModels) > 0 {
		out = append(out, timedSelected(ChannelChat, "selected_model", opts.ChatModel, func() (string, string, error) {
			if chatErr != nil {
				return "", "", chatErr
			}
			sel := strings.TrimSpace(selected)
			if err := checkSelectedModel(opts, sel); err != nil {
				return "", "", err
			}
			return sel, sel, nil
		}))
	}
	for _, r := range out {
		if r.Status == StatusFail {
			return out, fmt.Errorf("chat smoke failed")
		}
	}
	return out, nil
}

// checkSelectedModel applies RequireSelectedModel and AllowedSelectedModels.
func checkSelectedModel(opts Options, selected string) error {
	if selected == "" {
		if opts.RequireSelectedModel {
			return fmt.Errorf("missing selected model (routing header or body model)")
		}
		if len(opts.AllowedSelectedModels) > 0 {
			return fmt.Errorf("missing selected model for allow-list check")
		}
		return nil
	}
	if len(opts.AllowedSelectedModels) > 0 && !selectedModelAllowed(selected, opts.AllowedSelectedModels) {
		return fmt.Errorf("selected model %q not in allow-list", selected)
	}
	return nil
}

func selectedModelAllowed(selected string, allowed []string) bool {
	for _, a := range allowed {
		if strings.TrimSpace(a) == selected {
			return true
		}
	}
	return false
}

func pingHealth(ctx context.Context, baseURL string) error {
	req, err := httpNewGet(ctx, baseURL+"/health")
	if err != nil {
		return fmt.Errorf("health: request: %w", err)
	}
	_, _, err = call(req, "health", probeBodyMax)
	return err
}

const (
	smokeHeaderRouterSelected     = "x-model-router-selected-model"
	smokeHeaderSwitchyardSelected = "x-switchyard-selected-model"
)

// pingRequest is the one-word chat request the chat and batch probes send.
type pingRequest struct {
	Model       string         `json:"model"`
	Messages    []pingMessage  `json:"messages"`
	MaxTokens   int            `json:"max_tokens"`
	Temperature float64        `json:"temperature"`
	Reasoning   *pingReasoning `json:"reasoning,omitempty"`
}

// pingReasoning is the reasoning.effort a probe sends when Options.ReasoningEffort
// is set.
type pingReasoning struct {
	Effort string `json:"effort"`
}

type pingMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func newPingRequest(model string, opts Options) pingRequest {
	req := pingRequest{
		Model:       model,
		Messages:    []pingMessage{{Role: "user", Content: "Reply with the single word: ok"}},
		MaxTokens:   opts.MaxTokens,
		Temperature: opts.Temperature,
	}
	if effort := strings.TrimSpace(opts.ReasoningEffort); effort != "" {
		req.Reasoning = &pingReasoning{Effort: effort}
	}
	return req
}

// chatReply is one chat probe answer: the first choice's content, the model the
// gateway says it selected, and the raw body for error detail.
type chatReply struct {
	content       string
	selectedModel string
	raw           []byte
}

func chatPing(ctx context.Context, opts Options, model string) (chatReply, error) {
	body, err := json.Marshal(newPingRequest(model, opts))
	if err != nil {
		return chatReply{}, fmt.Errorf("chat: encode: %w", err)
	}
	req, err := httpNewPost(ctx, opts.BaseURL+"/v1/chat/completions", string(body))
	if err != nil {
		return chatReply{}, fmt.Errorf("chat: request: %w", err)
	}
	raw, hdr, err := call(req, "chat", probeBodyMax)
	if err != nil {
		return chatReply{}, err
	}
	selectedModel := extractSmokeSelectedModel(hdr, raw)
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return chatReply{}, fmt.Errorf("chat: decode: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return chatReply{}, fmt.Errorf("chat: no choices")
	}
	return chatReply{content: parsed.Choices[0].Message.Content, selectedModel: selectedModel, raw: raw}, nil
}

func extractSmokeSelectedModel(hdr map[string][]string, body []byte) string {
	get := func(key string) string {
		for k, vals := range hdr {
			if strings.EqualFold(k, key) && len(vals) > 0 {
				return strings.TrimSpace(vals[0])
			}
		}
		return ""
	}
	for _, key := range []string{smokeHeaderRouterSelected, smokeHeaderSwitchyardSelected} {
		if v := get(key); v != "" {
			return v
		}
	}
	var root struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	return strings.TrimSpace(root.Model)
}
