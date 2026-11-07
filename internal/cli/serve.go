package cli

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway"
	"github.com/behaviorengineering/polypus/internal/observability"
)

// serveOpts are serve's flags; empty or zero keeps the environment's value.
type serveOpts struct {
	host, backend string
	port          int
}

// runServe starts the gateway and blocks until SIGINT, SIGTERM or a serve failure.
// Its error carries the exit code: 2 for bad options or tracing config, 1 for a
// key store, tracing or serve failure.
func runServe(cio cmdIO, o serveOpts) error {
	const name = "polypus serve"
	opts, err := resolveServeOptions(o)
	if err != nil {
		return &exitError{code: 2, msg: name + ": " + err.Error(), err: err}
	}
	// The service logs its startup and runtime through one structured logger. The
	// gateway loads the router config once and logs what it serves.
	logger := slog.Default()
	logger.Info("polypus gateway", "url", opts.GatewayBaseURL())
	if path, err := warnIfKeyless(logger); err != nil {
		logger.Error("polypus serve: admin keys", "err", err, "path", path)
		return exitStatus(1)
	}
	return withTracingOpts(cio.ctx, name, tracingOptions{flush: serveFlushBudget, logger: logger},
		func(ctx context.Context, otelCfg observability.Config) error {
			// SIGINT and SIGTERM end ctx, and Serve then drains and shuts down.
			ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			if err := gateway.ListenAndServe(ctx, opts, gateway.WithLogger(logger),
				gateway.WithObservability(otelCfg.ActiveDumpDir(), otelCfg.SkipPaths),
				gateway.WithStreamChunkDebug(otelCfg.StreamChunkDebug)); err != nil {
				logger.Error("polypus serve failed", "err", err, "config", opts.ConfigPath)
				return exitStatus(1)
			}
			return nil
		})
}

// keylessHint is the command that locks a gateway running without access keys.
const keylessHint = "polypus admin-key generate --name <name>"

// warnIfKeyless reads the admin key store serve's gateway uses and, when it holds
// no key, warns once that the gateway answers anyone who can reach it. It returns
// the store path it read (empty when the path itself cannot be resolved), so a
// failure names the store too. A store that cannot be read is an error: the
// gateway would fail on it too.
func warnIfKeyless(logger *slog.Logger) (string, error) {
	path, err := config.ResolveAdminKeysPath()
	if err != nil {
		return "", err
	}
	store, err := openAdminKeyStoreAt(path)
	if err != nil {
		return path, err
	}
	records, err := store.List()
	if err != nil {
		return path, err
	}
	if len(records) == 0 {
		logger.Warn("polypus: no access keys; anyone who can reach this gateway can use it, including admin routes",
			"hint", keylessHint, "path", path)
	}
	return path, nil
}

// resolveServeOptions reads the serve options from the environment and applies the
// command's flags over them. --backend counts as the operator's choice, like
// POLYPUS_BACKEND_URL, so it replaces the config's mlx_local base_url.
func resolveServeOptions(o serveOpts) (config.ServeOptions, error) {
	opts, err := config.LoadServeOptions()
	if err != nil {
		return config.ServeOptions{}, err
	}
	if o.host != "" {
		opts.Host = o.host
	}
	if o.port > 0 {
		opts.Port = o.port
	}
	if o.backend != "" {
		opts.BackendURL = o.backend
	}
	return opts, nil
}
