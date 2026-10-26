package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/observability"
	"github.com/behaviorengineering/polypus/pkg/polypus"
)

func runServeWithOpts(ctx context.Context, host string, port int, backend string) int {
	opts, err := resolveServeOptions(host, port, backend)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus serve: %v\n", err)
		return 2
	}
	// The service logs its startup and runtime through one structured logger.
	logger := slog.Default()
	logger.Info("polypus gateway", "url", "http://"+opts.ListenAddr()+"/")
	rcfg, err := config.LoadRouterConfig(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus serve: router config: %v\n", err)
		return 1
	}
	logStartup(logger, rcfg)
	return withObservability(logger, func(otelCfg observability.Config) int {
		// SIGINT and SIGTERM end ctx, and Serve then drains and shuts down.
		ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := polypus.Serve(ctx, opts, polypus.WithLogger(logger),
			polypus.WithObservability(otelCfg.ActiveDumpDir(), otelCfg.SkipPaths)); err != nil {
			fmt.Fprintf(os.Stderr, "polypus serve: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "polypus serve: otel config: %v\n", err)
		return 2
	}
	otelCfg.Logger = logger
	shutdownOTEL, err := observability.Init(otelCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "polypus serve: otel: %v\n", err)
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

// logStartup logs each capability's default backend, the backends and the routers.
func logStartup(logger *slog.Logger, rcfg config.RouterConfig) {
	for _, c := range []struct {
		name string
		cap  config.CapabilityBackend
	}{
		{"chat_backend", rcfg.Chat},
		{"vision_backend", rcfg.Vision},
		{"embed_backend", rcfg.Embed},
		{"tts_backend", rcfg.TTS},
		{"stt_backend", rcfg.STT},
		{"proxy_backend", rcfg.Proxy},
		{"systemone_backend", rcfg.SystemOne},
	} {
		logger.Info("capability", "capability", c.name, "enabled", c.cap.Enabled, "default", c.cap.Default)
	}
	for _, id := range rcfg.BackendIDs() {
		b := rcfg.Backends[id]
		logger.Info("backend", "id", id, "base_url", b.BaseURL, "capabilities", b.Capabilities)
	}
	if len(rcfg.Routers) > 0 {
		logger.Info("routers", "count", len(rcfg.Routers))
	}
}
