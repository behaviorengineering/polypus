package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := Config{HTTP: srv.Client(), Token: "test", BaseURL: srv.URL, OwnerRepo: "acme/polypus"}.CreateClient()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTagCommitPeelsAnnotatedTag(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/repos/acme/polypus/git/ref/tags/v1.2.3":
			_, _ = w.Write([]byte(`{"object":{"type":"tag","sha":"tagsha"}}`))
		case "/repos/acme/polypus/git/tags/tagsha":
			_, _ = w.Write([]byte(`{"object":{"sha":"commitsha"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	sha, err := c.TagCommit(testContext(t), "v1.2.3")
	if err != nil || sha != "commitsha" {
		t.Fatalf("sha %q err %v", sha, err)
	}
}

func TestTagCommitNotFound(t *testing.T) {
	c := testClient(t, http.NotFound)
	_, err := c.TagCommit(testContext(t), "v9.9.9")
	if !errors.Is(err, ErrNotFound) || derrors.CodeOf(err) != derrors.CodeNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "/git/ref/tags/v9.9.9") {
		t.Fatalf("err = %v, want the URL named", err)
	}
}

func TestWorkflowRunsRetriesServerErrorOnce(t *testing.T) {
	var calls atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path != "/repos/acme/polypus/actions/workflows/docker-release.yml/runs" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":7,"status":"completed","conclusion":"success","head_branch":"v1.2.3","created_at":"2026-01-01T00:00:00Z"}]}`))
	})
	runs, err := c.WorkflowRuns(testContext(t), "docker-release.yml", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != 7 || runs[0].CreatedAt.IsZero() {
		t.Fatalf("runs %+v", runs)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls %d, want one retry by the outbound policy and no nested retry", calls.Load())
	}
}

func TestWorkflowRunsRejectsBadCreatedAt(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":7,"html_url":"https://x/run/7","created_at":"yesterday"}]}`))
	})
	_, err := c.WorkflowRuns(testContext(t), "docker-release.yml", 20)
	if err == nil || !strings.Contains(err.Error(), "created_at") {
		t.Fatalf("err = %v, want a bad created_at refused", err)
	}
}

func TestRunJobs(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[{"name":"verify","conclusion":"success"}]}`))
	})
	jobs, err := c.RunJobs(testContext(t), 9)
	if err != nil || len(jobs) != 1 || jobs[0].Name != "verify" || jobs[0].Conclusion != "success" {
		t.Fatalf("jobs %+v err %v", jobs, err)
	}
}

func TestCreateClientNeedsToken(t *testing.T) {
	if _, err := (Config{OwnerRepo: "a/b"}).CreateClient(); derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a missing token refused", err)
	}
}

func TestGetNeedsDeadline(t *testing.T) {
	c := testClient(t, http.NotFound)
	if _, err := c.RunJobs(context.Background(), 1); derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("err = %v, want a missing deadline refused", err)
	}
}
