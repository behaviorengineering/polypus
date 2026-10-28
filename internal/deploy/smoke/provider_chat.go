package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

func runProviderChat(ctx context.Context, ep endpoint, model, reasoningEffort string) error {
	start := time.Now()
	body, err := providerChatBody(model, reasoningEffort, 64, 0.2)
	if err != nil {
		return fmt.Errorf("smoke %s: %w", model, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("smoke %s: %w", model, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if ep.key != "" {
		req.Header.Set("Authorization", "Bearer "+ep.key)
	}
	resp, err := outbound.DoOnce(ctx, outbound.DepSmoke, http.DefaultClient, req)
	if err != nil {
		return fmt.Errorf("smoke %s: %w", model, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return fmt.Errorf("smoke %s: read body: %w", model, readErr)
	}
	ms := time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Printf("smoke: channel=chat probe=provider model=%s status=fail ms=%d detail=chat status %d: %s\n",
			model, ms, resp.StatusCode, truncateDetail(string(raw)))
		return fmt.Errorf("smoke %s: chat status %d: %s", model, resp.StatusCode, truncateDetail(string(raw)))
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		fmt.Printf("smoke: channel=chat probe=provider model=%s status=fail ms=%d detail=%s\n", model, ms, err)
		return fmt.Errorf("smoke %s: parse response: %w", model, err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		detail := truncateDetail(string(raw))
		fmt.Printf("smoke: channel=chat probe=provider model=%s status=fail ms=%d detail=empty content: %s\n", model, ms, detail)
		return fmt.Errorf("smoke %s: empty content: %s", model, detail)
	}
	fmt.Printf("smoke: channel=chat probe=provider model=%s status=pass ms=%d detail=ok\n", model, ms)
	return nil
}

func providerChatBody(model, reasoningEffort string, maxTokens int, temperature float64) ([]byte, error) {
	payload := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with the single word: ok"},
		},
		"max_tokens":  maxTokens,
		"temperature": temperature,
	}
	if effort := strings.TrimSpace(reasoningEffort); effort != "" {
		payload["reasoning"] = map[string]string{"effort": effort}
	}
	return json.Marshal(payload)
}

func truncateDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:197] + "..."
	}
	return s
}
