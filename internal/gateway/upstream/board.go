// Package upstream provides per-target circuit breakers for Polypus dials
// (Switchyard, OpenAI-shaped leaves, Cloudflare).
package upstream

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/ratelimit"
)

// Well-known breaker names.
const (
	NameSwitchyard = "switchyard"
)

// Breaker policy, shared with HTTP Retry-After on breaker refusals.
const (
	// OpenTimeout is how long a tripped breaker stays open before a trial call.
	OpenTimeout = 30 * time.Second
	// halfOpenMaxRequests is the trial calls a half-open breaker lets through; that
	// many successes close it again.
	halfOpenMaxRequests = uint(1)
	// ConsecutiveFailuresTrip is the run of upstream failures that opens a breaker.
	ConsecutiveFailuresTrip = uint(3)
)

// ErrOpen and ErrHalfOpen are the causes UnavailableError wraps when a breaker
// refuses a call: open, or half-open with its trial call already in flight.
var (
	ErrOpen     = errors.New("circuit breaker is open")
	ErrHalfOpen = errors.New("circuit breaker is half-open")
)

// ErrResponseWritten means the upstream HTTP status was already copied to the
// client; callers must not write another error body. Whether it counts against
// the breaker follows the wrapped domain code (CodeCanceled: the client left).
var ErrResponseWritten = errors.New("upstream: response already written")

// Board holds lazily created circuit breakers keyed by upstream name
// (backend id or NameSwitchyard). The zero value is ready to use.
type Board struct {
	mu sync.Mutex
	cb map[string]circuitbreaker.CircuitBreaker[any]
	// openDelay overrides OpenTimeout (tests); zero means OpenTimeout.
	openDelay time.Duration
}

// NewBoard returns an empty breaker board.
func NewBoard() *Board {
	return &Board{cb: make(map[string]circuitbreaker.CircuitBreaker[any])}
}

// Execute runs fn under the named breaker. A nil Board runs fn directly. A call the
// breaker refuses returns UnavailableError without running fn; any other error is
// fn's own, unchanged. Only an error backendFault blames on the backend counts
// against the breaker; ctx is the call's context, which says whether a deadline
// was the caller's choice (config.WithCallerBudget).
func (b *Board) Execute(ctx context.Context, name string, fn func() error) error {
	if b == nil {
		return fn()
	}
	name = normalizeName(name)
	cb := b.breaker(name)
	ran := false
	var fnErr error
	err := failsafe.With[any](cb).Run(func() error {
		ran = true
		fnErr = fn()
		if backendFault(ctx, fnErr) {
			return fnErr
		}
		// Not the backend's doing: the breaker records the call as answered.
		return nil
	})
	if ran {
		return fnErr
	}
	if err == nil || !errors.Is(err, circuitbreaker.ErrOpen) {
		return err
	}
	// Read the state at refusal time: a half-open breaker refuses while its trial
	// call is in flight.
	if cb.IsHalfOpen() {
		return UnavailableError(name, ErrHalfOpen)
	}
	return UnavailableError(name, ErrOpen)
}

// Unavailable reports whether err is a breaker refusal (open, or half-open with its
// trial call in flight).
func Unavailable(err error) bool {
	return errors.Is(err, ErrOpen) || errors.Is(err, ErrHalfOpen)
}

// ResponseWritten reports whether the dial already wrote the client response.
func ResponseWritten(err error) bool {
	return errors.Is(err, ErrResponseWritten)
}

func (b *Board) breaker(name string) circuitbreaker.CircuitBreaker[any] {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cb == nil {
		b.cb = make(map[string]circuitbreaker.CircuitBreaker[any])
	}
	if c, ok := b.cb[name]; ok {
		return c
	}
	delay := b.openDelay
	if delay <= 0 {
		delay = OpenTimeout
	}
	// Count based: it opens when the last ConsecutiveFailuresTrip calls all failed,
	// so any success in between keeps it closed.
	c := circuitbreaker.NewBuilder[any]().
		// Execute hands the breaker only backend faults, so every error counts.
		HandleIf(func(_ any, err error) bool { return err != nil }).
		WithFailureThreshold(ConsecutiveFailuresTrip).
		WithDelay(delay).
		WithSuccessThreshold(halfOpenMaxRequests).
		Build()
	b.cb[name] = c
	return c
}

// unknownUpstream names an upstream the error did not identify.
const unknownUpstream = "unknown"

func normalizeName(name string) string {
	if name == "" {
		return unknownUpstream
	}
	return name
}

// UnavailableError is the error for a call the breaker for name refused; err is
// ErrOpen or ErrHalfOpen.
func UnavailableError(name string, err error) error {
	return derrors.Wrap(err, derrors.CodeUnavailable, "upstream.Execute", "upstream "+name+" unavailable").
		With(FieldUpstream, name)
}

// FieldUpstream is the error field naming the upstream whose breaker refused a call.
const FieldUpstream = "upstream"

// backendFault reports whether err is the backend's doing, so it counts against
// the backend's breaker: the backend could not be reached or failed (CodeUnavailable,
// unless the backend answered with a 4xx about the request), or it ran past the
// gateway's own hop budget (CodeTimeout on a context the caller did not cut short).
// Rate limits, other caller errors, cancels and errors raised inside Polypus
// (NotReady such as a missing credential, FailedPrecondition, Internal,
// Unimplemented) never count, and neither does an error with no domain code.
func backendFault(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	// CodeOf reads an error with no domain code as CodeInternal.
	switch derrors.CodeOf(err) {
	case derrors.CodeUnavailable:
		return !answeredClientError(err)
	case derrors.CodeTimeout:
		// A deadline the caller set (X-Polypus-Timeout shorter than the hop budget)
		// is the caller's choice, not a slow backend.
		return !config.CallerBudgetFrom(ctx)
	case derrors.CodeRateLimited, derrors.CodeInvalid, derrors.CodeUnauthorized,
		derrors.CodeNotFound, derrors.CodeConflict, derrors.CodeCanceled,
		derrors.CodeNotReady, derrors.CodeFailedPrecondition, derrors.CodeInternal,
		derrors.CodeUnimplemented:
		return false
	default:
		return false
	}
}

// answeredClientError reports an upstream answer with a 4xx status: the backend is
// up and refused the request, whatever code the error was given.
func answeredClientError(err error) bool {
	status, convErr := strconv.Atoi(derrors.Field(err, ratelimit.FieldStatus))
	return convErr == nil && status >= 400 && status < 500
}
