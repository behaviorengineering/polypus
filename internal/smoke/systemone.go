package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func runSystemOne(ctx context.Context, opts Options) ([]Result, error) {
	if !opts.RequireCF && strings.TrimSpace(os.Getenv("CF_AI_API_KEY")) == "" {
		row := Result{
			Channel: ChannelSystemOne,
			Probe:   "evaluate",
			Model:   opts.SystemOneModel,
			Status:  "skip",
			Detail:  "CF_AI_API_KEY unset",
		}
		return []Result{row}, nil
	}
	row := timed(ChannelSystemOne, "evaluate", opts.SystemOneModel, func() (string, error) {
		return systemOnePing(ctx, opts)
	})
	if row.Status == "fail" {
		return []Result{row}, fmt.Errorf("systemone smoke failed")
	}
	return []Result{row}, nil
}

func systemOnePing(ctx context.Context, opts Options) (string, error) {
	payload := map[string]any{
		"model": opts.SystemOneModel,
		"state": "Help! My payouts have been failing for 3 days.",
		"questions": map[string]any{
			"is_urgent": map[string]any{
				"type":         "noul",
				"instructions": "Does this convey urgency?",
				"criteria": map[string]string{
					"true":  "Explicitly time-sensitive",
					"false": "No urgency expressed",
				},
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := httpNewPost(ctx, opts.BaseURL+"/v1/systemone", string(raw))
	if err != nil {
		return "", err
	}
	resp, err := httpDefaultDo(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ioReadAllLimit(resp.Body, 1<<20)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("systemone status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	var parsed struct {
		Answers map[string]json.RawMessage `json:"answers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Answers) == 0 {
		return "", fmt.Errorf("empty answers")
	}
	if _, ok := parsed.Answers["is_urgent"]; !ok {
		return "", fmt.Errorf("missing is_urgent answer")
	}
	return "answers ok", nil
}
