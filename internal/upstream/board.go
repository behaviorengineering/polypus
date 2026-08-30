// Package upstream provides per-target circuit breakers for Polypus dials
// (Switchyard, OpenAI-shaped leaves, Cloudflare).
package upstream

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sony/gobreaker"
)

// Well-known breaker names.
const (
	NameSwitchyard = "switchyard"
)

// ErrResponseWritten means the upstream HTTP status was already copied to the
// client; callers must not write another error body. It still counts as a
// breaker failure when status >= 500.
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
		MaxRequests: 1,
		Interval:    0,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 3
		},
	})
	b.cb[name] = c
	return c
}

func normalizeName(name string) string {
	if name == "" {
		return "unknown"
	}
	return name
}

func mapExecuteErr(name string, err error) error {
	if err == nil {
		return nil
	}
	if Unavailable(err) {
		return fmt.Errorf("upstream %s unavailable: %w", name, err)
	}
	return err
}
