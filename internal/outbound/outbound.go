// Package outbound is the failsafe-go seam for outbound HTTP and exec: retry with
// backoff and jitter for classified transients, and one circuit breaker per remote
// dependency. Clients send through Policies.Do or Policies.DoOnce, never through
// http.Client.Do directly.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// ErrAbort marks a non-retryable outbound failure.
var ErrAbort = errors.New("outbound: non-retryable")

// errRetry marks a failure worth retrying that the policies cannot recognise by type,
// such as a docker daemon that is not ready yet. Callers mark one with Retry.
var errRetry = errors.New("outbound: retryable")

// Retry marks err as worth another attempt.
func Retry(err error) error {
	if err == nil {
		return fmt.Errorf("%w: nil", errRetry)
	}
	return fmt.Errorf("%w: %w", errRetry, err)
}

// Dependency names for breaker keys.
const (
	DepHealth       = "health"
	DepDockerDaemon = "docker-daemon"
	DepCloudflare   = "cloudflare"
	DepGemini       = "gemini"
	DepAnthropic    = "anthropic"
	DepOpenRouter   = "openrouter"
	DepSmoke        = "smoke"
	DepGateway      = "gateway"
	DepGitHub       = "github"
	DepGHCR         = "ghcr"
	DepRegistry     = "registry"
)

// Abort wraps err so failsafe stops retrying.
func Abort(err error) error {
	if err == nil {
		return fmt.Errorf("%w: nil", ErrAbort)
	}
	return fmt.Errorf("%w: %w", ErrAbort, err)
}

// httpStatusRetryable reports whether an HTTP status should be retried.
func httpStatusRetryable(status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	return status >= 500 && status <= 599
}

// HTTPStatusPermanent reports whether an HTTP status must not be retried.
func HTTPStatusPermanent(status int) bool {
	if status == http.StatusTooManyRequests {
		return false
	}
	if status >= 200 && status < 300 {
		return false
	}
	if status >= 500 {
		return false
	}
	return status >= 400 && status < 500
}

// transportRetryable reports whether a transport-level error should be retried. It
// reads error types only (a network timeout, a reset, refused or broken connection,
// a cut-off body, a temporary DNS failure), never the error text, so an error that
// merely mentions "timeout" or "eof" is not retried.
func transportRetryable(err error) bool {
	if err == nil || errors.Is(err, ErrAbort) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.EOF) ||
		isTemporaryDNS(err)
}

// isTemporaryDNS reports a DNS lookup that may succeed on another attempt.
func isTemporaryDNS(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && (dnsErr.IsTemporary || dnsErr.IsTimeout)
}

func validateCtx(ctx context.Context) error {
	if err := requireCtx(ctx); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return derrors.New(derrors.CodeFailedPrecondition, "outbound", "missing deadline")
	}
	return nil
}

// requireCtx fails closed on a nil context, without asking for a deadline.
func requireCtx(ctx context.Context) error {
	if ctx == nil {
		return derrors.New(derrors.CodeInvalid, "outbound", "context required")
	}
	return nil
}

// Policies holds the circuit breakers for a set of outbound dependencies and the
// retry policy they share. A gateway builds its own, so tests and gateways never
// share breaker state; calls without one use the process default. The zero value is
// ready to use, with the production backoff.
type Policies struct {
	mu       sync.Mutex
	breakers map[string]failsafe.Policy[any]
	// retry is the retry policy; nil means productionRetry.
	retry failsafe.Policy[any]
}

// Backoff is the delay range between retries: the first retry waits Min, each
// later one twice the last, capped at Max, all with 20% jitter. A Retry-After on
// the upstream answer replaces it. A zero field takes the production value (1 s
// and 8 s).
type Backoff struct {
	Min, Max time.Duration
}

// Production backoff and retry budget.
const (
	defaultBackoffMin = time.Second
	defaultBackoffMax = 8 * time.Second
	maxRetries        = 3
	jitterFactor      = 0.2
)

// NewPolicies returns an empty policy set with the production backoff.
func NewPolicies() *Policies {
	return NewPoliciesWith(Backoff{})
}

// NewPoliciesWith returns an empty policy set that retries on backoff b. Tests pass
// a millisecond range so retry paths run without real waits.
func NewPoliciesWith(b Backoff) *Policies {
	return &Policies{breakers: map[string]failsafe.Policy[any]{}, retry: newRetryPolicy(b)}
}

// defaultPolicies serves the package-level Run, Do and DoOnce.
var defaultPolicies = NewPolicies()

// Scoped names one instance of a dependency, such as one backend of a provider kind,
// so it gets its own breaker: Scoped(DepAnthropic, "claude_team").
func Scoped(dep, scope string) string {
	if strings.TrimSpace(scope) == "" {
		return dep
	}
	return dep + "/" + scope
}

// depOf is the dependency kind of a name built by Scoped.
func depOf(name string) string {
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i]
	}
	return name
}

func (p *Policies) orDefault() *Policies {
	if p == nil {
		return defaultPolicies
	}
	return p
}

func (p *Policies) breakerFor(name string) failsafe.Policy[any] {
	switch depOf(name) {
	case DepCloudflare, DepGemini, DepAnthropic, DepOpenRouter, DepGitHub, DepGHCR, DepRegistry:
	default:
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.breakers == nil {
		p.breakers = map[string]failsafe.Policy[any]{}
	}
	if b, ok := p.breakers[name]; ok {
		return b
	}
	b := circuitbreaker.NewBuilder[any]().
		HandleIf(func(_ any, err error) bool {
			return retryableErr(err)
		}).
		WithFailureThreshold(5).
		WithDelay(30 * time.Second).
		Build()
	p.breakers[name] = b
	return b
}

// retryableErr reports a failure worth another attempt: a transport error, an HTTP
// 429 or 5xx, or one the caller marked with Retry. Abort wins over all of them.
func retryableErr(err error) bool {
	if err == nil || errors.Is(err, ErrAbort) {
		return false
	}
	if errors.Is(err, errRetry) || transportRetryable(err) {
		return true
	}
	var se *statusError
	return errors.As(err, &se) && httpStatusRetryable(se.code)
}

// statusError is an HTTP answer the policies judge by its code. retryAfter is the
// upstream's Retry-After, 0 when it sent none.
type statusError struct {
	code       int
	retryAfter time.Duration
}

func (e *statusError) Error() string { return fmt.Sprintf("http status %d", e.code) }

// productionRetry serves a zero-value Policies. A retry policy holds no state
// between executions, so one instance serves every dependency.
var productionRetry = newRetryPolicy(Backoff{})

// newRetryPolicy builds the retry policy for backoff b. The delay before each retry
// is the upstream's Retry-After when the last answer carried one, else b.
func newRetryPolicy(b Backoff) failsafe.Policy[any] {
	if b.Min <= 0 {
		b.Min = defaultBackoffMin
	}
	if b.Max <= 0 {
		b.Max = defaultBackoffMax
	}
	b.Max = max(b.Max, b.Min)
	return retrypolicy.NewBuilder[any]().
		HandleIf(func(_ any, err error) bool {
			return retryableErr(err)
		}).
		WithMaxRetries(maxRetries).
		WithDelayFunc(func(exec failsafe.ExecutionAttempt[any]) time.Duration {
			return b.delay(exec.Retries(), exec.LastError())
		}).
		Build()
}

// delay is the wait before the retry that follows retries earlier ones, after an
// attempt that failed with lastErr. An upstream Retry-After wins as sent, with no
// jitter, because the upstream named the time; otherwise the wait doubles from Min
// up to Max, with jitter so callers that failed together do not retry together.
func (b Backoff) delay(retries int, lastErr error) time.Duration {
	var se *statusError
	if errors.As(lastErr, &se) && se.retryAfter > 0 {
		return se.retryAfter
	}
	d := b.Min
	for i := 0; i < retries && d < b.Max; i++ {
		d *= 2
	}
	d = min(d, b.Max)
	// Jitter only spreads retries; it needs no cryptographic source.
	factor := 1 + (2*rand.Float64()-1)*jitterFactor //nolint:gosec // G404: retry jitter, not a secret
	return time.Duration(float64(d) * factor)
}

func (p *Policies) retryPolicy() failsafe.Policy[any] {
	if p.retry == nil {
		return productionRetry
	}
	return p.retry
}

func (p *Policies) policies(name string) []failsafe.Policy[any] {
	return append(p.breakerOnly(name), p.retryPolicy())
}

// breakerOnly is the policy set for a request that must not be retried.
func (p *Policies) breakerOnly(name string) []failsafe.Policy[any] {
	if b := p.breakerFor(name); b != nil {
		return []failsafe.Policy[any]{b}
	}
	return nil
}

// Run executes fn under outbound retry (and breaker when name is a shared remote dep),
// with the process default policies.
func Run(ctx context.Context, name string, fn func() error) error {
	return defaultPolicies.Run(ctx, name, fn)
}

// Do sends req under the process default policies; see Policies.Do.
func Do(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	return defaultPolicies.Do(ctx, name, client, req)
}

// DoOnce sends req once under the process default policies; see Policies.DoOnce.
func DoOnce(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	return defaultPolicies.DoOnce(ctx, name, client, req)
}

// Run executes fn under retry and the breaker for name.
func (p *Policies) Run(ctx context.Context, name string, fn func() error) error {
	if err := validateCtx(ctx); err != nil {
		return err
	}
	return failsafe.With(p.orDefault().policies(name)...).
		WithContext(ctx).
		Run(fn)
}

// Do performs one HTTP request under retry and the breaker for name. req is cloned
// per attempt. When every attempt ends in an HTTP status (permanent, or retryable
// with retries spent), Do returns the last response with a nil error for the caller
// to map; the caller then owns resp.Body and closes it.
//
// A retryable answer (429 or 5xx) that carries Retry-After sets the wait before the
// next attempt. When that wait would outlast ctx, Do stops at once and returns that
// answer, with its Retry-After, and a nil error.
//
// When ctx ends while Do waits between retries, Do returns the last response
// together with a CodeTimeout (deadline) or CodeCanceled error, so the cause is the
// deadline and not the upstream status. On that path Do has already closed the
// body: resp carries the status and headers (such as Retry-After) and an empty
// body, so a caller that returns on the error without closing leaks nothing.
// CloseResponse stays safe to call on any resp Do returns.
//
// When ctx ends while a request is in flight, the error is CodeTimeout or
// CodeCanceled as well; any other transport or breaker failure is CodeUnavailable.
func (p *Policies) Do(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	p = p.orDefault()
	if err := validateCtx(ctx); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "http client is required")
	}
	if req == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "nil request")
	}
	pol := p.policies(name)
	if !replayable(req) {
		// A body without GetBody is consumed by the first attempt; retrying would
		// send it empty, so this request gets one attempt.
		pol = p.breakerOnly(name)
	}
	var resp *http.Response
	err := failsafe.With(pol...).
		WithContext(ctx).
		Run(func() error {
			if resp != nil {
				// The answer this attempt replaces is never read: drain it so the
				// transport can reuse the connection for this very retry.
				drainAndClose(resp)
				resp = nil
			}
			attempt, err := cloneRequest(ctx, req)
			if err != nil {
				return Abort(err)
			}
			r, err := client.Do(attempt) //nolint:gosec // G704: targets are operator-configured backend and registry URLs
			if err != nil {
				return err
			}
			retryStatus := depOf(name) != DepHealth
			if retryStatus {
				if HTTPStatusPermanent(r.StatusCode) {
					resp = r
					return Abort(&statusError{code: r.StatusCode})
				}
				if httpStatusRetryable(r.StatusCode) {
					// Kept until the next attempt closes it, so the caller still gets
					// the last upstream answer (status, body, Retry-After) when
					// retries run out.
					resp = r
					return retryableStatus(ctx, r)
				}
			}
			resp = r
			return nil
		})
	if err != nil {
		if resp != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The deadline ended the retries: keep the last upstream status and
				// headers for the caller, say why it stopped, and close the body here
				// so a caller that returns on the error cannot leak the connection.
				CloseResponse(resp)
				resp.Body = http.NoBody
				return resp, derrors.Wrap(ctxErr, contextCode(ctxErr), "outbound."+name, "deadline during retries").
					With("dependency", name).With("last_status", strconv.Itoa(resp.StatusCode))
			}
			return resp, nil
		}
		return nil, dialError(ctx, name, err)
	}
	return resp, nil
}

// retryableStatus is the error for a retryable answer r. When r names a Retry-After
// the retry waits that long; when that wait would outlast ctx, the error aborts the
// retries so the caller gets the answer and its Retry-After at once, rather than a
// spent deadline.
func retryableStatus(ctx context.Context, r *http.Response) error {
	se := &statusError{code: r.StatusCode}
	wait, ok := ratelimit.RetryAfter(r.Header, time.Now())
	if !ok {
		return se
	}
	se.retryAfter = wait
	if deadline, has := ctx.Deadline(); has && time.Until(deadline) <= wait {
		return Abort(se)
	}
	return se
}

// CloseResponse closes resp's body when there is one. Do can return a response with
// an error; a caller that returns on the error closes the response here. A failed
// close of a body nobody reads has nothing left to report.
func CloseResponse(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

// drainLimit caps how much of an unread body drainAndClose reads so the connection
// can go back to the pool; a longer body is cheaper to drop with its connection.
const drainLimit = 64 << 10

// drainAndClose reads what is left of resp's body, up to drainLimit, and closes it.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	defer CloseResponse(resp)
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, drainLimit)); err != nil {
		// A body that fails mid-read cannot be reused; the deferred close still runs.
		return
	}
}

// contextCode maps a context error to its domain code: a deadline is CodeTimeout,
// a cancel is CodeCanceled.
func contextCode(err error) derrors.Code {
	if errors.Is(err, context.Canceled) {
		return derrors.CodeCanceled
	}
	return derrors.CodeTimeout
}

// DoOnce sends req exactly once under the breaker for name, with no retry. Use it
// for paid or non-idempotent calls (inference, batch submit), whose upstream status
// and Retry-After the caller acts on. Like Do, it fails closed without a deadline.
// An HTTP 5xx counts as a breaker failure, and the response still goes back to the
// caller with a nil error.
func (p *Policies) DoOnce(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	if err := validateCtx(ctx); err != nil {
		return nil, err
	}
	return p.orDefault().once(ctx, name, client, req)
}

// once is one attempt under the breaker for name, for DoOnce. ctx is already
// validated.
func (p *Policies) once(ctx context.Context, name string, client *http.Client, req *http.Request) (*http.Response, error) {
	if client == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "http client is required")
	}
	if req == nil {
		return nil, derrors.New(derrors.CodeInvalid, "outbound", "nil request")
	}
	var resp *http.Response
	err := failsafe.With(p.breakerOnly(name)...).
		WithContext(ctx).
		Run(func() error {
			r, err := client.Do(req.WithContext(ctx)) //nolint:gosec // G704: targets are operator-configured backend URLs
			if err != nil {
				return err
			}
			resp = r
			if breakerStatusFailure(r.StatusCode) {
				// The breaker counts the failure; the caller still maps the answer.
				return &statusError{code: r.StatusCode}
			}
			return nil
		})
	if err != nil {
		if resp != nil {
			return resp, nil
		}
		return nil, dialError(ctx, name, err)
	}
	return resp, nil
}

// breakerStatusFailure reports an HTTP status that counts against a single-attempt
// call's breaker: a 5xx says the upstream is unhealthy. A 429 does not, so a quota
// refusal keeps reaching the caller with its Retry-After instead of opening the
// breaker.
func breakerStatusFailure(status int) bool {
	return status >= 500 && status <= 599
}

// dialError types a failure that left no response for dep name. When ctx has ended,
// the caller's deadline or cancel stopped the request, so the code is CodeTimeout or
// CodeCanceled whatever the transport reported (a deadline can surface as a plain
// i/o timeout). Otherwise it is CodeUnavailable, keeping any domain code already in
// the chain.
func dialError(ctx context.Context, name string, err error) error {
	code := derrors.CodeOr(err, derrors.CodeUnavailable)
	if ctxErr := ctx.Err(); ctxErr != nil {
		code = contextCode(ctxErr)
	}
	return derrors.Wrap(err, code, "outbound."+name, "dial")
}

// cloneRequest copies req for one attempt. Clone shares the body, so a replayable
// body is reopened from GetBody for every attempt.
func cloneRequest(ctx context.Context, req *http.Request) (*http.Request, error) {
	clone := req.Clone(ctx)
	if req.GetBody != nil && req.Body != nil && req.Body != http.NoBody {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("outbound: reopen request body: %w", err)
		}
		clone.Body = body
	}
	return clone, nil
}

// replayable reports whether every attempt can send the full body.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}
