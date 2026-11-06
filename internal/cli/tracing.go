package cli

import (
	"context"
	"log/slog"
	"time"

	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/spf13/cobra"
)

// tracingFlushBudget bounds the span flush when a traced command returns.
const tracingFlushBudget = 5 * time.Second

// withTracing starts the tracer provider for one job command, runs it, then
// flushes the spans. Job commands that make outbound calls (deploy, smokes) use it
// so a hang shows the client side, not only the gateway's server spans. Export
// failures never fail the command.
func withTracing(cmd *cobra.Command, run func(context.Context) error) error {
	name := cmd.CommandPath()
	otelCfg, err := observability.LoadConfig()
	if err != nil {
		return &exitError{code: 2, msg: name + ": tracing config: " + err.Error(), err: err}
	}
	otelCfg.Logger = slog.Default()
	shutdown, err := observability.Init(otelCfg)
	if err != nil {
		return &exitError{code: 1, msg: name + ": tracing: " + err.Error(), err: err}
	}
	defer func() {
		// The command's context may already be done; the flush gets its own budget.
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(cmd.Context()), tracingFlushBudget)
		defer cancel()
		if err := shutdown(flushCtx); err != nil {
			slog.Default().Warn("tracing flush failed", "command", name, "err", err)
		}
	}()
	return run(cmd.Context())
}
