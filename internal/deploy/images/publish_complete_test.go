package images

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVerifyPublishCompleteTagRefRetries503(t *testing.T) {
	var tagCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			if tagCalls.Add(1) == 1 {
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"object":{"sha":"abc123"}}`))
		case strings.Contains(r.URL.Path, "/actions/runs/9/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"name":"verify","conclusion":"success"}]}`))
		case strings.Contains(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":9,"name":"Auto patch release","conclusion":"success"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	err := VerifyPublishComplete(testContext(t), "0.2.40", WaitOptions{
		OwnerRepo:        "acme/polypus",
		HTTPClient:       srv.Client(),
		Token:            "test",
		GitHubAPIBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tagCalls.Load() < 2 {
		t.Fatalf("expected tag ref retry, calls=%d", tagCalls.Load())
	}
}

func TestVerifyPublishCompleteOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_, _ = w.Write([]byte(`{"object":{"sha":"abc123"}}`))
		case strings.Contains(r.URL.Path, "/actions/runs/9/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"name":"verify","conclusion":"success"}]}`))
		case strings.Contains(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":9,"name":"Auto patch release","conclusion":"success"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	err := VerifyPublishComplete(testContext(t), "0.2.40", WaitOptions{
		OwnerRepo:        "acme/polypus",
		HTTPClient:       srv.Client(),
		Token:            "test",
		GitHubAPIBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestVerifyPublishCompleteMissingVerifyJob(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/git/ref/tags/"):
			_, _ = w.Write([]byte(`{"object":{"sha":"abc123"}}`))
		case strings.Contains(r.URL.Path, "/actions/runs/9/jobs"):
			_, _ = w.Write([]byte(`{"jobs":[{"name":"verify","conclusion":"failure"}]}`))
		case strings.Contains(r.URL.Path, "/actions/runs"):
			_, _ = w.Write([]byte(`{"workflow_runs":[{"id":9,"name":"Auto patch release","conclusion":"success"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	err := VerifyPublishComplete(testContext(t), "0.2.40", WaitOptions{
		OwnerRepo:        "acme/polypus",
		HTTPClient:       srv.Client(),
		Token:            "test",
		GitHubAPIBaseURL: srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "publish incomplete") {
		t.Fatalf("expected publish incomplete, got %v", err)
	}
}

func TestVerifyPublishCompleteRebuildOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "docker-release.yml") {
			_, _ = w.Write([]byte(`{"workflow_runs":[{"name":"Docker release","display_title":"0.2.40-r1","conclusion":"success"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
	}))
	t.Cleanup(srv.Close)

	err := VerifyPublishComplete(testContext(t), "0.2.40-r1", WaitOptions{
		OwnerRepo:        "acme/polypus",
		HTTPClient:       srv.Client(),
		Token:            "test",
		GitHubAPIBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
}
