package smoke

import (
	"context"

	"github.com/behaviorengineering/polypus/internal/smoke/otelcol"
)

// CollectorOptions configures OTLP fan-out smoke against otelcol, Phoenix, and HyperDX.
type CollectorOptions = otelcol.CollectorOptions

// DefaultCollectorPollInterval is the delay between backend polls.
const DefaultCollectorPollInterval = otelcol.DefaultCollectorPollInterval

// DefaultCollectorSmokeTimeout is the collector smoke budget.
const DefaultCollectorSmokeTimeout = otelcol.DefaultCollectorSmokeTimeout

// RunCollector exports two synthetic traces through otelcol and verifies Phoenix / HyperDX routing.
func RunCollector(ctx context.Context, opts CollectorOptions) error {
	return otelcol.RunCollector(ctx, opts)
}
