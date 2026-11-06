package cli

import (
	"context"
	"io"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway"
	"github.com/behaviorengineering/polypus/internal/observability"
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
	if err := warnIfKeyless(logger); err != nil {
		logger.Error("polypus serve: admin keys", "err", err)
		return 1
	}
	return withObservability(logger, func(otelCfg observability.Config) int {
		// SIGINT and SIGTERM end ctx, and Serve then drains and shuts down.
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := gateway.ListenAndServe(ctx, opts, gateway.WithLogger(logger),
			gateway.WithObservability(otelCfg.ActiveDumpDir(), otelCfg.SkipPaths)); err != nil {
			logger.Error("polypus serve failed", "err", err, "config", opts.ConfigPath)
			return 1
		}
		return 0
	})
}

// keylessHint is the command that locks a gateway running without access keys.
const keylessHint = "polypus admin-key generate --name <name>"

// warnIfKeyless reads the admin key store serve's gateway uses and, when it holds
// no key, warns once that the gateway answers anyone who can reach it. A store
// that cannot be read is an error: the gateway would fail on it too.
func warnIfKeyless(logger *slog.Logger) error {
	store, err := openAdminKeyStore()
	if err != nil {
		return err
	}
	records, err := store.List()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		logger.Warn("polypus: no access keys; anyone who can reach this gateway can use it, including admin routes",
			"hint", keylessHint)
	}
	return nil
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
