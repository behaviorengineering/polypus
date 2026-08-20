// polypus-chat-smoke: L1 transport probes against Polypus gateway (standalone).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	baseURL := flag.String("base-url", envOr("POLYPUS_BASE_URL", "http://127.0.0.1:1320"), "Polypus gateway base URL")
	model := flag.String("model", "", "model id (backend/downstream); required unless -list")
	list := flag.Bool("list", false, "print default chat smoke models and exit")
	flag.Parse()

	defaults := smokeDefaults{
		BaseURL:     strings.TrimRight(*baseURL, "/"),
		Temperature: 0.1,
		MaxTokens:   256,
	}
	models := defaultChatModels()
	if *list {
		for _, m := range models {
			fmt.Println(m)
		}
		return
	}
	if strings.TrimSpace(*model) == "" {
		fmt.Fprintln(os.Stderr, "polypus-chat-smoke: -model required (or -list)")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	probes := []string{"ping", "content_nonempty", "thinking_policy"}
	for _, probe := range probes {
		row := runProbe(ctx, defaults, strings.TrimSpace(*model), probe)
		enc := json.NewEncoder(os.Stdout)
		_ = enc.Encode(row)
		if row.Status == "fail" {
			os.Exit(1)
		}
	}
}

type smokeDefaults struct {
	BaseURL     string
	Temperature float64
	MaxTokens   int
}

type smokeRow struct {
	Model  string `json:"model"`
	Tier   string `json:"tier"`
	Probe  string `json:"probe"`
	Status string `json:"status"`
	Ms     int64  `json:"ms"`
	Detail string `json:"detail"`
}

func defaultChatModels() []string {
	// Standalone defaults when no Consilium manifest is present.
	return []string{
		"cf_local/@cf/google/gemma-4-26b-a4b-it",
		"cf_local/@cf/zai-org/glm-4.7-flash",
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func runProbe(ctx context.Context, defaults smokeDefaults, model, probe string) smokeRow {
	start := time.Now()
	row := smokeRow{Model: model, Tier: "L1", Probe: probe, Status: "pass"}
	// Minimal inline probes (mirrors Consilium harness L1 without cross-module import).
	switch probe {
	case "ping":
		if err := pingHealth(ctx, defaults.BaseURL); err != nil {
			row.Status = "fail"
			row.Detail = err.Error()
		} else {
			row.Detail = "health ok"
		}
	case "content_nonempty", "thinking_policy":
		content, reasoning, err := chatPing(ctx, defaults, model)
		if err != nil {
			row.Status = "fail"
			row.Detail = err.Error()
		} else if strings.TrimSpace(content) == "" {
			row.Status = "fail"
			if probe == "thinking_policy" && strings.TrimSpace(reasoning) != "" {
				row.Detail = "content empty; reasoning populated"
			} else {
				row.Detail = "empty content"
			}
		} else {
			row.Detail = strings.TrimSpace(content)
			if len(row.Detail) > 60 {
				row.Detail = row.Detail[:57] + "..."
			}
		}
	default:
		row.Status = "skip"
		row.Detail = "unknown probe"
	}
	row.Ms = time.Since(start).Milliseconds()
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
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health status %d", resp.StatusCode)
	}
	return nil
}

func chatPing(ctx context.Context, defaults smokeDefaults, model string) (content, reasoning string, err error) {
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply with the single word: ok"}],"max_tokens":%d,"temperature":%g}`,
		model, defaults.MaxTokens, defaults.Temperature)
	req, err := httpNewPost(ctx, defaults.BaseURL+"/v1/chat/completions", body)
	if err != nil {
		return "", "", err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
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
