package cli

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/spf13/cobra"
)

// Flush budgets for the spans a command leaves behind. serve has run for hours and
// may hold a large batch; a job command has a handful of spans.
const (
	jobFlushBudget   = 5 * time.Second
	serveFlushBudget = 10 * time.Second
)

// tracingOptions tunes withTracingOpts for one command.
type tracingOptions struct {
	// flush bounds the span flush when the command returns.
	flush time.Duration
	// logger receives the otel line and flush warnings; nil uses slog.Default.
	logger *slog.Logger
}

// withTracing starts the tracer provider for one job command, runs it, then
// flushes the spans. Job commands that make outbound calls (deploy, smokes) use it
// so a hang shows the client side, not only the gateway's server spans. Export
// failures never fail the command.
func withTracing(cmd *cobra.Command, run func(context.Context) error) error {
	return withTracingOpts(cmd.Context(), cmd.CommandPath(), tracingOptions{flush: jobFlushBudget},
		func(ctx context.Context, _ observability.Config) error { return run(ctx) })
}

// withTracingOpts is the one place a command starts and flushes tracing: it loads
// the config, starts the provider, logs where spans and failure dumps go, runs run
// with ctx and the config, then flushes within o.flush. A tracing config error is
// exit 2 and a provider that cannot start is exit 1, both as *exitError; run's own
// error is returned as it is.
func withTracingOpts(ctx context.Context, name string, o tracingOptions, run func(context.Context, observability.Config) error) error {
	logger := o.logger
	if logger == nil {
		logger = slog.Default()
	}
	otelCfg, err := observability.LoadConfig()
	if err != nil {
		return &exitError{code: 2, msg: name + ": tracing config: " + err.Error(), err: err}
	}
	otelCfg.Logger = logger
	shutdown, err := observability.Init(otelCfg)
	if err != nil {
		return &exitError{code: 1, msg: name + ": tracing: " + err.Error(), err: err}
	}
	if otelCfg.Enabled {
		logger.Info("otel", "command", name, "service", otelCfg.ServiceName, "otlp", otelCfg.OTLPEndpoint,
			"dump", absDumpDir(otelCfg.ActiveDumpDir()), "skip", otelCfg.SkipPaths)
	}
	defer func() {
		// The command's context may already be done; the flush keeps its values and
		// gets its own budget.
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.flush)
		defer cancel()
		if err := shutdown(flushCtx); err != nil {
			logger.Warn("tracing flush failed", "command", name, "err", err)
		}
	}()
	return run(ctx, otelCfg)
}

// absDumpDir names the failure dump root as an absolute path, so an operator who
// ran a job from another directory can still find the dumps. A path that cannot be
// made absolute is returned as it is.
func absDumpDir(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}
