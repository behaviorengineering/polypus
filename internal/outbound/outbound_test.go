package outbound

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunMissingDeadline(t *testing.T) {
	err := Run(context.Background(), DepGitHub, func() error { return nil })
	if err == nil || err.Error() != "outbound: missing deadline" {
		t.Fatalf("expected missing deadline, got %v", err)
	}
}

func TestRunNilContext(t *testing.T) {
	var ctx context.Context // nil on purpose
	err := Run(ctx, DepGitHub, func() error { return nil })
	if err == nil || err.Error() != "outbound: context required" {
		t.Fatalf("got %v", err)
	}
}

func TestDoRetries503(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Do(ctx, DepGitHub, srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected retry, calls=%d", calls.Load())
	}
}

func TestDoAbort404(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Do(ctx, DepGitHub, srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected single attempt, calls=%d", calls.Load())
	}
}

func TestDoNeverDialsWithoutDeadline(t *testing.T) {
	var dialed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed.Store(true)
	}))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := Do(context.Background(), DepGitHub, srv.Client(), req)
	if err == nil || err.Error() != "outbound: missing deadline" {
		t.Fatalf("got %v dialed=%v", err, dialed.Load())
	}
	if dialed.Load() {
		t.Fatal("should not dial")
	}
}
