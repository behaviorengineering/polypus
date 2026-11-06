// Package upstream provides per-target circuit breakers for Polypus dials
// (Switchyard, OpenAI-shaped leaves, Cloudflare).
package upstream

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
// fn's own, unchanged.
func (b *Board) Execute(name string, fn func() error) error {
	if b == nil {
		return fn()
	}
	name = normalizeName(name)
	cb := b.breaker(name)
	ran := false
	err := failsafe.With[any](cb).Run(func() error {
		ran = true
		return fn()
	})
	if err == nil || ran || !errors.Is(err, circuitbreaker.ErrOpen) {
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

// StatusFailure returns ErrResponseWritten for 5xx so the breaker trips after
// the proxy has already forwarded the upstream body. 2xx/4xx return nil.
func StatusFailure(status int) error {
	if status >= 500 {
		return fmt.Errorf("upstream status %d: %w", status, ErrResponseWritten)
	}
	return nil
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
		HandleIf(func(_ any, err error) bool {
			// Quota and capacity 429s and caller errors (4xx) are caller-visible, not a dead upstream.
			return err != nil && !callerVisible(derrors.CodeOf(err))
		}).
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

// callerVisible reports codes that describe the request, not the upstream's health.
// CodeCanceled is the caller going away (closed stream, cancelled request), so it
// never counts against the backend.
func callerVisible(code derrors.Code) bool {
	switch code {
	case derrors.CodeRateLimited, derrors.CodeInvalid, derrors.CodeUnauthorized,
		derrors.CodeNotFound, derrors.CodeConflict, derrors.CodeCanceled:
		return true
	case derrors.CodeTimeout, derrors.CodeFailedPrecondition,
		derrors.CodeUnavailable, derrors.CodeNotReady, derrors.CodeInternal, derrors.CodeUnimplemented:
		return false
	default:
		return false
	}
}
