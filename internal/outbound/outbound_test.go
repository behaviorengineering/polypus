package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
	resp, err := NewPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := NewPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := NewPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := NewPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
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
	_, err := NewPolicies().Do(context.Background(), DepCloudflare, srv.Client(), req)
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
		{errors.New("upstream said: request timeout exceeded"), false},
		{errors.New("unexpected eof in config"), false},
		{errors.New("connection reset by peer"), false},
		{&url.Error{Op: "Post", URL: "http://x", Err: io.EOF}, true},
		{fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), true},
		{&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, true},
		{&net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}, true},
		{&net.OpError{Op: "write", Net: "tcp", Err: os.NewSyscallError("write", syscall.EPIPE)}, true},
		{&net.DNSError{Err: "server misbehaving", Name: "x", IsTemporary: true}, true},
		{&net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}, false},
		{context.DeadlineExceeded, false},
	} {
		if got := retryableErr(tc.err); got != tc.want {
			t.Fatalf("%v: got %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestZeroValuePoliciesWork(t *testing.T) {
	var p Policies
	if p.breakerFor(DepCloudflare) == nil {
		t.Fatal("a zero Policies must build breakers, not panic on a nil map")
	}
}

func TestDoRetriesOnlyStatusForScopedHealth(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewPolicies().Do(ctx, Scoped(DepHealth, "gateway"), srv.Client(), req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable || calls.Load() != 1 {
		t.Fatalf("a scoped health probe must not retry a status: status=%d calls=%d", resp.StatusCode, calls.Load())
	}
}

// statusServer answers every request with status and counts the requests.
func statusServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "body")
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// sendOnce is DoOnce.
type sendOnce func(p *Policies, ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error)

var onceSenders = map[string]sendOnce{
	"DoOnce": (*Policies).DoOnce,
}

func TestOnceCounts5xxAgainstTheBreaker(t *testing.T) {
	for name, send := range onceSenders {
		t.Run(name, func(t *testing.T) {
			srv, calls := statusServer(t, http.StatusServiceUnavailable)
			p := NewPolicies()
			dep := Scoped(DepCloudflare, "cf_test")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for i := range 5 {
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("{}"))
				if err != nil {
					t.Fatal(err)
				}
				resp, err := send(p, ctx, dep, srv.Client(), req)
				if err != nil {
					t.Fatalf("attempt %d: a 5xx must reach the caller as a response: %v", i, err)
				}
				raw, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusServiceUnavailable || string(raw) != "body" {
					t.Fatalf("attempt %d: status=%d body=%q", i, resp.StatusCode, raw)
				}
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := send(p, ctx, dep, srv.Client(), req)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("five 5xx answers must open the breaker")
			}
			if derrors.CodeOf(err) != derrors.CodeUnavailable {
				t.Fatalf("an open breaker is CodeUnavailable, got %v", err)
			}
			if calls.Load() != 5 {
				t.Fatalf("each call is one attempt and the open breaker sends nothing, calls=%d", calls.Load())
			}
		})
	}
}

func TestOnceDoesNotCount429AgainstTheBreaker(t *testing.T) {
	for name, send := range onceSenders {
		t.Run(name, func(t *testing.T) {
			srv, calls := statusServer(t, http.StatusTooManyRequests)
			p := NewPolicies()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for i := range 7 {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := send(p, ctx, DepCloudflare, srv.Client(), req)
				if err != nil {
					t.Fatalf("attempt %d: a 429 must reach the caller: %v", i, err)
				}
				_ = resp.Body.Close()
			}
			if calls.Load() != 7 {
				t.Fatalf("calls=%d", calls.Load())
			}
		})
	}
}

func TestDoOnceDialFailureIsUnavailable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // A closed port refuses the dial.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = DoOnce(ctx, DepGateway, http.DefaultClient, req)
	if derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("a refused dial is CodeUnavailable, got %v", err)
	}
}

// TestDoDeadlineDuringBackoffReturnsTimeoutAndResponse checks a deadline that ends
// while Do waits to retry reads as a timeout, not an upstream 5xx, and that the last
// upstream answer still reaches the caller.
func TestDoDeadlineDuringBackoffReturnsTimeoutAndResponse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	// The first backoff is at least 0.8s (1s less 20% jitter), well past this deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
	defer CloseResponse(resp)
	if derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("code %s, want %s: %v", derrors.CodeOf(err), derrors.CodeTimeout, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the cause should be the deadline: %v", err)
	}
	if derrors.Field(err, "last_status") != "503" {
		t.Fatalf("last_status %q", derrors.Field(err, "last_status"))
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("want the last 503 response, got %v", resp)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || !strings.Contains(string(body), "busy") {
		t.Fatalf("body %q, %v", body, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls %d, want 1 before the deadline", calls.Load())
	}
}

// TestDoRetriesExhaustedReturnsResponseNilError checks retries that run out with
// time to spare still hand back the last answer with a nil error for the caller to
// map. It waits through the real backoff (about 7s), so it runs in parallel.
func TestDoRetriesExhaustedReturnsResponseNilError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "5")
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// DepGateway retries status codes and has no breaker to cut the retries short.
	resp, err := NewPolicies().Do(ctx, DepGateway, srv.Client(), req)
	defer CloseResponse(resp)
	if err != nil {
		t.Fatalf("exhausted retries should return a nil error, got %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("want the last 503 with Retry-After, got %v", resp)
	}
	if calls.Load() != 4 {
		t.Fatalf("calls %d, want 4 (one try and three retries)", calls.Load())
	}
}
