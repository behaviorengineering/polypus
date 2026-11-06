package upstream

import (
	"slices"
	"strings"

	"github.com/failsafe-go/failsafe-go/circuitbreaker"
)

// Snapshot is a point-in-time circuit breaker view. The counts are failsafe-go's
// breaker metrics: they cover the current state (closed or half-open), or the
// closed state that tripped while the breaker is open, and reset on each change.
type Snapshot struct {
	Name        string  `json:"name"`
	State       string  `json:"state"`
	Executions  uint    `json:"executions"`
	Successes   uint    `json:"successes"`
	Failures    uint    `json:"failures"`
	FailureRate float64 `json:"failure_rate"`
}

// Snapshot returns breaker state for all lazily created upstream names.
func (b *Board) Snapshot() []Snapshot {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Snapshot, 0, len(b.cb))
	for name, cb := range b.cb {
		if cb == nil {
			continue
		}
		m := cb.Metrics()
		out = append(out, Snapshot{
			Name:        name,
			State:       breakerStateString(cb.State()),
			Executions:  m.Executions(),
			Successes:   m.Successes(),
			Failures:    m.Failures(),
			FailureRate: m.FailureRate(),
		})
	}
	// A stable order, so /health/upstreams lists the same way on every call.
	slices.SortFunc(out, func(a, b Snapshot) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Breaker state names, as /health/upstreams and dial failures report them.
const (
	stateClosed   = "closed"
	stateOpen     = "open"
	stateHalfOpen = "half-open"
	stateUnknown  = "unknown"
)

func breakerStateString(state circuitbreaker.State) string {
	switch state {
	case circuitbreaker.ClosedState:
		return stateClosed
	case circuitbreaker.OpenState:
		return stateOpen
	case circuitbreaker.HalfOpenState:
		return stateHalfOpen
	default:
		return stateUnknown
	}
}
