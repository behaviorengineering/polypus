package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func runSystemOne(ctx context.Context, opts Options) ([]Result, error) {
	row := timed(ChannelSystemOne, "evaluate", opts.SystemOneModel, func() (string, error) {
		return systemOnePing(ctx, opts)
	})
	if row.Status == StatusFail && !opts.RequireCF && isSystemOneBillingSkip(row.Detail) {
		row.Status = StatusSkip
		row.Detail = "Workers AI third-party billing: " + row.Detail
		return []Result{row}, nil
	}
	if row.Status == StatusFail {
		return []Result{row}, fmt.Errorf("systemone smoke failed")
	}
	return []Result{row}, nil
}

func isSystemOneBillingSkip(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "insufficient balance") ||
		strings.Contains(d, "status 402") ||
		strings.Contains(d, "use byok")
}

// systemOneRequest is a SystemOne decision request.
type systemOneRequest struct {
	Model     string                       `json:"model"`
	State     string                       `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

type systemOneQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

func systemOnePing(ctx context.Context, opts Options) (string, error) {
	raw, err := json.Marshal(systemOneRequest{
		Model: opts.SystemOneModel,
		State: "Help! My payouts have been failing for 3 days.",
		Questions: map[string]systemOneQuestion{
			"is_urgent": {
				Type:         "noul",
				Instructions: "Does this convey urgency?",
				Criteria:     map[string]string{"true": "Explicitly time-sensitive", "false": "No urgency expressed"},
			},
		},
	})
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
