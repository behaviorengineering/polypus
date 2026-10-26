package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDoRetryResendsTheFullBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if len(bodies) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(`{"q":1}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Do(ctx, DepGitHub, srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if len(bodies) != 2 || bodies[0] != `{"q":1}` || bodies[1] != `{"q":1}` {
		t.Fatalf("each attempt must send the full body, got %q", bodies)
	}
}

func TestDoSendsAnUnreplayableBodyOnce(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, io.NopCloser(strings.NewReader(`{"q":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	if req.GetBody != nil {
		t.Fatal("test needs a body without GetBody")
	}
	resp, err := Do(ctx, DepGitHub, srv.Client(), req)
	if err == nil {
		_ = resp.Body.Close()
	}
	if calls.Load() != 1 {
		t.Fatalf("an unreplayable body must be sent once, calls=%d", calls.Load())
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
