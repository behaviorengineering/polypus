// Package upstream provides per-target circuit breakers for Polypus dials
// (Switchyard, OpenAI-shaped leaves, Cloudflare).
package upstream

import (
	"errors"
	"fmt"
	"sync"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/sony/gobreaker"
)

// Well-known breaker names.
const (
	NameSwitchyard = "switchyard"
)

// Gobreaker policy shared with HTTP Retry-After on breaker rejects.
const (
	OpenTimeout             = 30 * time.Second
	halfOpenMaxRequests     = uint32(1)
	ConsecutiveFailuresTrip = uint32(3)
)

// ErrResponseWritten means the upstream HTTP status was already copied to the
// client; callers must not write another error body. Whether it counts against
// the breaker follows the wrapped domain code (CodeCanceled: the client left).
var ErrResponseWritten = errors.New("upstream: response already written")

// Board holds lazily created circuit breakers keyed by upstream name
// (backend id or NameSwitchyard).
type Board struct {
	mu sync.Mutex
	cb map[string]*gobreaker.CircuitBreaker
}

// NewBoard returns an empty breaker board.
func NewBoard() *Board {
	return &Board{cb: make(map[string]*gobreaker.CircuitBreaker)}
}

// Execute runs fn under the named breaker. A nil Board runs fn directly.
func (b *Board) Execute(name string, fn func() error) error {
	if b == nil {
		return fn()
	}
	name = normalizeName(name)
	_, err := b.breaker(name).Execute(func() (interface{}, error) {
		return nil, fn()
	})
	return mapExecuteErr(name, err)
}

// Unavailable reports whether err is a fail-open (circuit open / half-open limit).
func Unavailable(err error) bool {
	return errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests)
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

func (b *Board) breaker(name string) *gobreaker.CircuitBreaker {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.cb[name]; ok {
		return c
	}
	c := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: halfOpenMaxRequests,
		Interval:    0,
		Timeout:     OpenTimeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= ConsecutiveFailuresTrip
		},
		IsSuccessful: func(err error) bool {
			if err == nil {
				return true
			}
			// Quota and capacity 429s and caller errors (4xx) are caller-visible, not a dead upstream.
			return callerVisible(derrors.CodeOf(err))
		},
	})
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

func mapExecuteErr(name string, err error) error {
	if err == nil {
		return nil
	}
	if Unavailable(err) {
		return UnavailableError(name, err)
	}
	return err
}

// UnavailableError is the error for a call the breaker for name refused; err is the
// breaker's (gobreaker.ErrOpenState or ErrTooManyRequests).
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
