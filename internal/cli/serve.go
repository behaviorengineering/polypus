package cli

import (
	"context"
	"io"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/pkg/polypus"
)

func runServeWithOpts(ctx context.Context, errOut io.Writer, host string, port int, backend string) int {
	opts, err := resolveServeOptions(host, port, backend)
	if err != nil {
		errorf(errOut, "polypus serve: %v\n", err)
		return 2
	}
	// The service logs its startup and runtime through one structured logger. The
	// gateway loads the router config once and logs what it serves.
	logger := slog.Default()
	logger.Info("polypus gateway", "url", opts.GatewayBaseURL())
	return withObservability(logger, func(otelCfg observability.Config) int {
		// SIGINT and SIGTERM end ctx, and Serve then drains and shuts down.
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := polypus.Serve(ctx, opts, polypus.WithLogger(logger),
			polypus.WithObservability(otelCfg.ActiveDumpDir(), otelCfg.SkipPaths)); err != nil {
			logger.Error("polypus serve failed", "err", err, "config", opts.ConfigPath)
			return 1
		}
		return 0
	})
}

// resolveServeOptions reads the serve options from the environment and applies the
// command's flags over them.
func resolveServeOptions(host string, port int, backend string) (config.ServeOptions, error) {
	opts, err := config.LoadServeOptions()
	if err != nil {
		return config.ServeOptions{}, err
	}
	if host != "" {
		opts.Host = host
	}
	if port > 0 {
		opts.Port = port
	}
	if backend != "" {
		opts.BackendURL = backend
	}
	return opts, nil
}

// withObservability starts tracing for run and flushes it when run returns. It
// returns run's exit code, or its own when tracing cannot start.
func withObservability(logger *slog.Logger, run func(observability.Config) int) int {
	otelCfg, err := observability.LoadConfig()
	if err != nil {
		logger.Error("otel config failed", "err", err)
		return 2
	}
	otelCfg.Logger = logger
	shutdownOTEL, err := observability.Init(otelCfg)
	if err != nil {
		logger.Error("otel start failed", "err", err)
		return 1
	}
	if otelCfg.Enabled {
		logger.Info("otel", "service", otelCfg.ServiceName, "otlp", otelCfg.OTLPEndpoint,
			"dump", otelCfg.DumpDir, "skip", otelCfg.SkipPaths)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := shutdownOTEL(ctx); err != nil {
			logger.Warn("otel shutdown", "err", err)
		}
	}()
	return run(otelCfg)
}
