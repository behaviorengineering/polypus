package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunMatchesVersion(t *testing.T) {
	r := workflowRun{HeadBranch: "v0.2.34"}
	if !runMatchesVersion(r, "v0.2.34", "0.2.34") {
		t.Fatal("expected head_branch match")
	}
	r2 := workflowRun{DisplayTitle: "Docker release v0.2.34"}
	if !runMatchesVersion(r2, "v0.2.34", "0.2.34") {
		t.Fatal("expected title match")
	}
	if runMatchesVersion(workflowRun{DisplayTitle: "v0.2.340"}, "v0.2.34", "0.2.34") {
		t.Fatal("should not match longer semver substring")
	}
}

func TestRunInProgress(t *testing.T) {
	if !runInProgress(workflowRun{Status: "in_progress"}) {
		t.Fatal("expected in progress")
	}
	if runInProgress(workflowRun{Status: "completed"}) {
		t.Fatal("expected not in progress")
	}
}

func TestApplyWaitDefaultsPreservesTimeout(t *testing.T) {
	opts := applyWaitDefaults(WaitOptions{Timeout: 5 * time.Minute})
	if opts.Timeout != 5*time.Minute {
		t.Fatalf("timeout: %v", opts.Timeout)
	}
	if opts.OwnerRepo == "" {
		t.Fatal("expected default owner repo")
	}
}

func TestWaitForImagesPollsWhenNoRunsYet(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if calls.Load() < 2 {
			_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"workflow_runs":[{"status":"completed","conclusion":"success","head_branch":"v0.2.34","html_url":"https://example/run","created_at":"2026-01-01T00:00:00Z"}]}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForImages(ctx, "0.2.34", WaitOptions{
		OwnerRepo:           "acme/polypus",
		WorkflowFiles:       []string{"docker-release.yml"},
		PollInterval:        20 * time.Millisecond,
		Timeout:             2 * time.Second,
		HTTPClient:          srv.Client(),
		Token:               "test",
		WorkflowRunsBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected poll retries, calls=%d", calls.Load())
	}
}
