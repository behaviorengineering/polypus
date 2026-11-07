package images

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/github"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestRunMatchesVersion(t *testing.T) {
	r := github.WorkflowRun{HeadBranch: "v0.2.34"}
	if !runMatchesVersion(r, "v0.2.34", "0.2.34") {
		t.Fatal("expected head_branch match")
	}
	r2 := github.WorkflowRun{DisplayTitle: "Docker release v0.2.34"}
	if !runMatchesVersion(r2, "v0.2.34", "0.2.34") {
		t.Fatal("expected title match")
	}
	if runMatchesVersion(github.WorkflowRun{DisplayTitle: "v0.2.340"}, "v0.2.34", "0.2.34") {
		t.Fatal("should not match longer semver substring")
	}
}

func TestRunInProgress(t *testing.T) {
	if !runInProgress(github.WorkflowRun{Status: "in_progress"}) {
		t.Fatal("expected in progress")
	}
	if runInProgress(github.WorkflowRun{Status: "completed"}) {
		t.Fatal("expected not in progress")
	}
}

func TestApplyWaitDefaults(t *testing.T) {
	opts := applyWaitDefaults(WaitOptions{PollInterval: time.Second})
	if opts.PollInterval != time.Second {
		t.Fatalf("poll interval: %v", opts.PollInterval)
	}
	if opts.OwnerRepo == "" || len(opts.WorkflowFiles) == 0 {
		t.Fatal("expected default owner repo and workflow files")
	}
}

// githubFor is a GitHub client for srv.
func githubFor(t *testing.T, srv *httptest.Server) *github.Client {
	t.Helper()
	gh, err := github.Config{HTTP: srv.Client(), BaseURL: srv.URL, Token: "test", OwnerRepo: "acme/polypus"}.CreateClient()
	if err != nil {
		t.Fatal(err)
	}
	return gh
}

func runsServer(t *testing.T, body func(call int32) string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body(n)))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func waitOpts(t *testing.T, srv *httptest.Server) WaitOptions {
	return WaitOptions{
		WorkflowFiles: []string{"docker-release.yml"},
		PollInterval:  20 * time.Millisecond,
		GitHub:        githubFor(t, srv),
	}
}

func TestWaitForImagesPollsWhenNoRunsYet(t *testing.T) {
	srv, calls := runsServer(t, func(n int32) string {
		if n < 2 {
			return `{"workflow_runs":[]}`
		}
		return `{"workflow_runs":[{"status":"completed","conclusion":"success","head_branch":"v0.2.34","html_url":"https://example/run","created_at":"2026-01-01T00:00:00Z"}]}`
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForImages(ctx, "0.2.34", waitOpts(t, srv)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected poll retries, calls=%d", calls.Load())
	}
}

func TestWaitForImagesNeedsADeadline(t *testing.T) {
	err := waitForImages(context.Background(), "0.2.34", WaitOptions{})
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a context without a deadline refused", err)
	}
}

func TestWaitForImagesTimesOutOnItsContext(t *testing.T) {
	srv, _ := runsServer(t, func(int32) string { return `{"workflow_runs":[]}` })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := waitForImages(ctx, "0.2.34", waitOpts(t, srv))
	if derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

// TestWaitForImagesEndedRunsFailFast checks every finished, unsuccessful
// conclusion ends the wait with the failed build, instead of waiting out the budget.
func TestWaitForImagesEndedRunsFailFast(t *testing.T) {
	for _, c := range []string{"failure", "cancelled", "timed_out", "action_required", "stale", "neutral", "skipped", "startup_failure", "something_new"} {
		t.Run(c, func(t *testing.T) {
			srv, _ := runsServer(t, func(int32) string {
				return `{"workflow_runs":[{"status":"completed","conclusion":"` + c + `","head_branch":"v0.2.34","html_url":"https://example/run","created_at":"2026-01-01T00:00:00Z"}]}`
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := waitForImages(ctx, "0.2.34", waitOpts(t, srv))
			if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
				t.Fatalf("err = %v, want the run's end reported", err)
			}
		})
	}
}

// TestWaitForImagesJudgesTheNewestRunOnly checks an older run still in progress
// does not hold a finished, green newest run.
func TestWaitForImagesJudgesTheNewestRunOnly(t *testing.T) {
	srv, calls := runsServer(t, func(int32) string {
		return `{"workflow_runs":[
			{"status":"in_progress","head_branch":"v0.2.34","html_url":"https://example/old","created_at":"2026-01-01T00:00:00Z"},
			{"status":"completed","conclusion":"success","head_branch":"v0.2.34","html_url":"https://example/new","created_at":"2026-01-02T00:00:00Z"}]}`
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitForImages(ctx, "0.2.34", waitOpts(t, srv)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("polled %d times, want one", calls.Load())
	}
}
