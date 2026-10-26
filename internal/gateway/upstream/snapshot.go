package upstream

import (
	"github.com/sony/gobreaker"
)

// Snapshot is a point-in-time circuit breaker view.
type Snapshot struct {
	Name                string `json:"name"`
	State               string `json:"state"`
	Requests            uint32 `json:"requests"`
	TotalSuccesses      uint32 `json:"total_successes"`
	TotalFailures       uint32 `json:"total_failures"`
	ConsecutiveFailures uint32 `json:"consecutive_failures"`
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
		counts := cb.Counts()
		out = append(out, Snapshot{
			Name:                name,
			State:               breakerStateString(cb.State()),
			Requests:            counts.Requests,
			TotalSuccesses:      counts.TotalSuccesses,
			TotalFailures:       counts.TotalFailures,
			ConsecutiveFailures: counts.ConsecutiveFailures,
		})
	}
	return out
}

func breakerStateString(state gobreaker.State) string {
	switch state {
	case gobreaker.StateClosed:
		return "closed"
	case gobreaker.StateOpen:
		return "open"
	case gobreaker.StateHalfOpen:
		return "half-open"
	default:
		return unknownUpstream
	}
}
