package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func runChat(ctx context.Context, opts Options) ([]Result, error) {
	ping := timed(ChannelChat, "ping", "", func() (string, error) {
		if err := pingHealth(ctx, opts.BaseURL); err != nil {
			return "", err
		}
		return "health ok", nil
	})
	content := timed(ChannelChat, "content_nonempty", opts.ChatModel, func() (string, error) {
		text, _, _, err := chatPing(ctx, opts, opts.ChatModel)
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
	if opts.RequireSelectedModel || len(opts.AllowedSelectedModels) > 0 {
		selected := timedSelected(ChannelChat, "selected_model", opts.ChatModel, func() (string, string, error) {
			_, _, selected, err := chatPing(ctx, opts, opts.ChatModel)
			if err != nil {
				return "", "", err
			}
			selected = strings.TrimSpace(selected)
			if opts.RequireSelectedModel && selected == "" {
				return "", "", fmt.Errorf("missing selected model (routing header or body model)")
			}
			if len(opts.AllowedSelectedModels) > 0 && selected != "" {
				if !selectedModelAllowed(selected, opts.AllowedSelectedModels) {
					return "", "", fmt.Errorf("selected model %q not in allow-list", selected)
				}
			}
			if len(opts.AllowedSelectedModels) > 0 && selected == "" {
				return "", "", fmt.Errorf("missing selected model for allow-list check")
			}
			return selected, selected, nil
		})
		out = append(out, selected)
	}
	var err error
	for _, r := range out {
		if r.Status == "fail" {
			err = fmt.Errorf("chat smoke failed")
			break
		}
	}
	return out, err
}

func selectedModelAllowed(selected string, allowed []string) bool {
	for _, a := range allowed {
		if strings.TrimSpace(a) == selected {
			return true
		}
	}
	return false
}

func timedSelected(channel, probe, model string, fn func() (string, string, error)) Result {
	start := time.Now()
	row := Result{Channel: channel, Probe: probe, Model: model, Status: "pass"}
	detail, selected, err := fn()
	row.Ms = time.Since(start).Milliseconds()
	if err != nil {
		row.Status = "fail"
		row.Detail = err.Error()
		return row
	}
	row.SelectedModel = selected
	row.Detail = detail
	return row
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

const (
	smokeHeaderRouterSelected     = "x-model-router-selected-model"
	smokeHeaderSwitchyardSelected = "x-switchyard-selected-model"
)

func chatPing(ctx context.Context, opts Options, model string) (content, reasoning, selectedModel string, err error) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply with the single word: ok"}],"max_tokens":%d,"temperature":%g}`,
		model, opts.MaxTokens, opts.Temperature)
	req, err := httpNewPost(ctx, opts.BaseURL+"/v1/chat/completions", body)
	if err != nil {
		return "", "", "", err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := ioReadAllLimit(resp.Body, 1<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", "", fmt.Errorf("chat status %d: %s", resp.StatusCode, string(raw))
	}
	selectedModel = extractSmokeSelectedModel(resp.Header, raw)
	var parsed struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", "", "", err
	}
	if len(parsed.Choices) == 0 {
		return "", "", "", fmt.Errorf("no choices")
	}
	msg := parsed.Choices[0].Message
	return msg.Content, msg.ReasoningContent, selectedModel, nil
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
