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

// fastPolicies retries on a millisecond backoff, so retry paths run without real
// waits.
func fastPolicies() *Policies {
	return NewPoliciesWith(Backoff{Min: time.Millisecond, Max: time.Millisecond})
}

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
	resp, err := fastPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
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
	resp, err := fastPolicies().Do(ctx, DepCloudflare, srv.Client(), req)
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
// while Do waits to retry reads as a timeout, not an upstream 5xx, that the last
// upstream status and headers still reach the caller, and that Do has closed the
// body itself so a caller that returns on the error leaks nothing.
func TestDoDeadlineDuringBackoffReturnsTimeoutAndResponse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Upstream", "last")
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	// The first backoff is at least 8s (10s less 20% jitter), well past this deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := NewPoliciesWith(Backoff{Min: 10 * time.Second}).Do(ctx, DepCloudflare, srv.Client(), req)
	// No CloseResponse on purpose: Do owns the body when it returns an error.
	if derrors.CodeOf(err) != derrors.CodeTimeout {
		t.Fatalf("code %s, want %s: %v", derrors.CodeOf(err), derrors.CodeTimeout, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the cause should be the deadline: %v", err)
	}
	if derrors.Field(err, "last_status") != "503" {
		t.Fatalf("last_status %q", derrors.Field(err, "last_status"))
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("X-Upstream") != "last" {
		t.Fatalf("want the last 503 response with its headers, got %v", resp)
	}
	if resp.Body != http.NoBody {
		t.Fatalf("Do must close the body it hands back with an error, got %T", resp.Body)
	}
	CloseResponse(resp) // Still safe on the closed response.
	if calls.Load() != 1 {
		t.Fatalf("calls %d, want 1 before the deadline", calls.Load())
	}
}

// TestDoRetriesExhaustedReturnsResponseNilError checks retries that run out with
// time to spare still hand back the last answer with a nil error for the caller to
// map.
func TestDoRetriesExhaustedReturnsResponseNilError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Upstream", "last")
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	// DepGateway retries status codes and has no breaker to cut the retries short.
	resp, err := fastPolicies().Do(ctx, DepGateway, srv.Client(), req)
	defer CloseResponse(resp)
	if err != nil {
		t.Fatalf("exhausted retries should return a nil error, got %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("X-Upstream") != "last" {
		t.Fatalf("want the last 503 with its headers, got %v", resp)
	}
	if calls.Load() != 4 {
		t.Fatalf("calls %d, want 4 (one try and three retries)", calls.Load())
	}
}

// TestDoReturnsA429AtOnceWhenRetryAfterOutlastsTheDeadline checks a Retry-After
// longer than the time left stops the retries: the caller gets the 429 and its
// Retry-After with a nil error, not a spent deadline.
func TestDoReturnsA429AtOnceWhenRetryAfterOutlastsTheDeadline(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := fastPolicies().Do(ctx, Scoped(DepGitHub, "api"), srv.Client(), req)
	defer CloseResponse(resp)
	if err != nil {
		t.Fatalf("want the 429 with a nil error, got %v", err)
	}
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "3600" {
		t.Fatalf("want the 429 with its Retry-After, got %v", resp)
	}
	if body, _ := io.ReadAll(resp.Body); !strings.Contains(string(body), "slow down") {
		t.Fatalf("the caller should still read the upstream body, got %q", body)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls %d, want 1: a wait past the deadline is not worth a retry", calls.Load())
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Do waited %s before giving up", time.Since(start))
	}
}

// TestDoRetriesA429WithoutRetryAfterOnTheBackoff checks a 429 with no usable
// Retry-After still retries on the backoff.
func TestDoRetriesA429WithoutRetryAfterOnTheBackoff(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"", "0", "soon"} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				if header != "" {
					w.Header().Set("Retry-After", header)
				}
				http.Error(w, "slow down", http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := fastPolicies().Do(ctx, DepGateway, srv.Client(), req)
		CloseResponse(resp)
		cancel()
		srv.Close()
		if err != nil || resp.StatusCode != http.StatusOK || calls.Load() != 2 {
			t.Fatalf("Retry-After %q: status=%v calls=%d err=%v", header, resp, calls.Load(), err)
		}
	}
}

func TestRetryableStatusHonoursRetryAfterThatFits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, tc := range []struct {
		header    string
		wantWait  time.Duration
		wantAbort bool
	}{
		{"", 0, false},
		{"2", 2 * time.Second, false},
		{"59", 59 * time.Second, false},
		{"61", 61 * time.Second, true},
		{time.Now().Add(2 * time.Hour).UTC().Format(http.TimeFormat), 0, true},
	} {
		r := &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}}
		if tc.header != "" {
			r.Header.Set("Retry-After", tc.header)
		}
		err := retryableStatus(ctx, r)
		if errors.Is(err, ErrAbort) != tc.wantAbort {
			t.Fatalf("Retry-After %q: abort=%v, want %v", tc.header, errors.Is(err, ErrAbort), tc.wantAbort)
		}
		var se *statusError
		if !errors.As(err, &se) || se.code != http.StatusTooManyRequests {
			t.Fatalf("Retry-After %q: want the status error, got %v", tc.header, err)
		}
		if tc.wantWait > 0 && se.retryAfter != tc.wantWait {
			t.Fatalf("Retry-After %q: wait %s, want %s", tc.header, se.retryAfter, tc.wantWait)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	b := Backoff{Min: time.Second, Max: 8 * time.Second}
	within := func(d, want time.Duration) bool {
		return d >= time.Duration(float64(want)*(1-jitterFactor)) && d <= time.Duration(float64(want)*(1+jitterFactor))
	}
	for retries, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second} {
		if d := b.delay(retries, &statusError{code: 503}); !within(d, want) {
			t.Fatalf("retries %d: delay %s, want %s within jitter", retries, d, want)
		}
	}
	if d := b.delay(0, &statusError{code: 429, retryAfter: 30 * time.Second}); d != 30*time.Second {
		t.Fatalf("Retry-After should set the delay exactly, got %s", d)
	}
	if d := b.delay(0, Abort(&statusError{code: 429, retryAfter: 3 * time.Second})); d != 3*time.Second {
		t.Fatalf("a wrapped status error keeps its Retry-After, got %s", d)
	}
}

func TestNewPoliciesWithFillsZeroBackoff(t *testing.T) {
	if NewPoliciesWith(Backoff{}).retry == nil || NewPolicies().retry == nil {
		t.Fatal("every constructor must set a retry policy")
	}
	var p Policies
	if p.retryPolicy() != productionRetry {
		t.Fatal("a zero Policies retries on the production policy")
	}
}

// TestInFlightContextEndIsTimeoutOrCanceled checks a deadline or cancel that ends a
// request while it is in flight is coded by the context, not as an unavailable
// upstream, for both Do and DoOnce.
func TestInFlightContextEndIsTimeoutOrCanceled(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	senders := map[string]func(p *Policies, ctx context.Context, req *http.Request) (*http.Response, error){
		"Do": func(p *Policies, ctx context.Context, req *http.Request) (*http.Response, error) {
			return p.Do(ctx, Scoped(DepOpenRouter, "or_test"), srv.Client(), req)
		},
		"DoOnce": func(p *Policies, ctx context.Context, req *http.Request) (*http.Response, error) {
			return p.DoOnce(ctx, Scoped(DepCloudflare, "cf_test"), srv.Client(), req)
		},
	}
	for name, send := range senders {
		t.Run(name+"/deadline", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := send(NewPolicies(), ctx, req)
			CloseResponse(resp)
			if derrors.CodeOf(err) != derrors.CodeTimeout {
				t.Fatalf("code %s, want %s: %v", derrors.CodeOf(err), derrors.CodeTimeout, err)
			}
		})
		t.Run(name+"/cancel", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			timer := time.AfterFunc(100*time.Millisecond, cancel)
			defer timer.Stop()
			resp, err := send(NewPolicies(), ctx, req)
			CloseResponse(resp)
			if derrors.CodeOf(err) != derrors.CodeCanceled {
				t.Fatalf("code %s, want %s: %v", derrors.CodeOf(err), derrors.CodeCanceled, err)
			}
		})
	}
}

// closeTracker is a body that records how much was read and whether it was closed.
type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return nil
}

func TestDrainAndCloseReadsTheRestAndCloses(t *testing.T) {
	r := strings.NewReader("unread 5xx body")
	body := &closeTracker{Reader: r}
	drainAndClose(&http.Response{Body: body})
	if !body.closed || r.Len() != 0 {
		t.Fatalf("closed=%v unread=%d", body.closed, r.Len())
	}
	drainAndClose(nil)
	drainAndClose(&http.Response{})
}
