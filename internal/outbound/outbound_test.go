package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunMissingDeadline(t *testing.T) {
	err := Run(context.Background(), DepCloudflare, func() error { return nil })
	if err == nil || err.Error() != "outbound: missing deadline" {
		t.Fatalf("expected missing deadline, got %v", err)
	}
}

func TestRunNilContext(t *testing.T) {
	var ctx context.Context // Nil on purpose.
	err := Run(ctx, DepCloudflare, func() error { return nil })
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
	resp, err := Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := Do(ctx, DepCloudflare, srv.Client(), req)
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
	_, err := Do(context.Background(), DepCloudflare, srv.Client(), req)
	if err == nil || err.Error() != "outbound: missing deadline" {
		t.Fatalf("got %v dialed=%v", err, dialed.Load())
	}
	if dialed.Load() {
		t.Fatal("should not dial")
	}
}

func TestBreakersArePerPolicySetAndPerBackend(t *testing.T) {
	a, b := NewPolicies(), NewPolicies()
	team := Scoped(DepAnthropic, "claude_team")
	personal := Scoped(DepAnthropic, "claude_personal")
	first, again := a.breakerFor(team), a.breakerFor(team)
	if first == nil || first != again {
		t.Fatal("a scoped provider dep gets one stable breaker")
	}
	if a.breakerFor(team) == a.breakerFor(personal) {
		t.Fatal("two backends of one provider must not share a breaker")
	}
	if a.breakerFor(team) == b.breakerFor(team) {
		t.Fatal("two policy sets (two gateways, two tests) must not share a breaker")
	}
	if a.breakerFor(Scoped(DepSmoke, "x")) != nil {
		t.Fatal("deps without a breaker stay without one when scoped")
	}
}

func TestRetryableErrReadsTypesNotText(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&statusError{code: 503}, true},
		{&statusError{code: 429}, true},
		{&statusError{code: 404}, false},
		{Retry(errors.New("daemon not ready")), true},
		{Abort(Retry(errors.New("both"))), false},
		{errors.New("http status 503, says some text"), false},
	} {
		if got := retryableErr(tc.err); got != tc.want {
			t.Fatalf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}
