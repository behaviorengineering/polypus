package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func runChat(ctx context.Context, opts Options) ([]Result, error) {
	ping := timed(ChannelChat, "ping", "", func() (string, error) {
		if err := pingHealth(ctx, opts.BaseURL); err != nil {
			return "", err
		}
		return "health ok", nil
	})
	content := timed(ChannelChat, "content_nonempty", opts.ChatModel, func() (string, error) {
		text, _, err := chatPing(ctx, opts, opts.ChatModel)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", fmt.Errorf("empty content")
		}
		d := strings.TrimSpace(text)
		if len(d) > 60 {
			d = d[:57] + "..."
		}
		return d, nil
	})
	out := []Result{ping, content}
	var err error
	if ping.Status == "fail" || content.Status == "fail" {
		err = fmt.Errorf("chat smoke failed")
	}
	return out, err
}

func pingHealth(ctx context.Context, baseURL string) error {
	req, err := httpNewGet(ctx, baseURL+"/health")
	if err != nil {
		return err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

func chatPing(ctx context.Context, opts Options, model string) (content, reasoning string, err error) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply with the single word: ok"}],"max_tokens":%d,"temperature":%g}`,
		model, opts.MaxTokens, opts.Temperature)
	req, err := httpNewPost(ctx, opts.BaseURL+"/v1/chat/completions", body)
	if err != nil {
		return "", "", err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := ioReadAllLimit(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("chat status %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", err
	}
	if len(parsed.Choices) == 0 {
		return "", "", fmt.Errorf("no choices")
	}
	msg := parsed.Choices[0].Message
	return msg.Content, msg.ReasoningContent, nil
}
