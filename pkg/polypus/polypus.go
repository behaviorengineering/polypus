// Package polypus is the public product façade for the Polypus inference gateway.
//
// Import this package from hosts and CI. Implementation and quality runners live
// under internal/, and the cmd binaries wire internal/cli directly. Nothing under
// internal/ imports this package, so it stays the outside hosts' contract.
package polypus

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway"
	"github.com/behaviorengineering/polypus/internal/smoke"
)

// ServeOptions is the gateway listen configuration. It aliases the internal
// options, so its exported fields are this package's contract:
// TestFacadeFieldSetsArePinned fails when one is added, removed or retyped, and
// that change is reviewed here.
type ServeOptions = config.ServeOptions

// HandlerOption configures Serve and NewHandler. Build one with the With* functions
// below. An option built from invalid input does not panic: Serve and NewHandler
// return its error (CodeInvalid) before they start anything.
type HandlerOption struct {
	apply gateway.HandlerOption
	err   error
}

// WithLogger sets the logger the gateway writes warnings and request failures to
// (slog.Default otherwise). A nil l makes Serve and NewHandler return an error.
func WithLogger(l *slog.Logger) HandlerOption {
	if l == nil {
		return HandlerOption{err: derrors.New(derrors.CodeInvalid, "polypus.WithLogger", "logger is nil")}
	}
	return HandlerOption{apply: gateway.WithLogger(l)}
}

// WithObservability sets where failure dumps land (empty turns them off) and the
// HTTP paths that get no SERVER span (nil keeps the default /health probes).
func WithObservability(dumpDir string, traceSkipPaths []string) HandlerOption {
	return HandlerOption{apply: gateway.WithObservability(dumpDir, traceSkipPaths)}
}

// WithAdminStateDir keeps the models allow overlay and the admin key store under dir
// instead of the XDG state directory. An empty dir makes Serve and NewHandler
// return an error.
func WithAdminStateDir(dir string) HandlerOption {
	if strings.TrimSpace(dir) == "" {
		return HandlerOption{err: derrors.New(derrors.CodeInvalid, "polypus.WithAdminStateDir", "state dir is empty")}
	}
	return HandlerOption{apply: gateway.WithAdminStateDir(dir)}
}

// gatewayOptions unwraps the options, joining every invalid one into one error.
func gatewayOptions(op string, options []HandlerOption) ([]gateway.HandlerOption, error) {
	out := make([]gateway.HandlerOption, 0, len(options))
	var errs []error
	for _, o := range options {
		if o.err != nil {
			errs = append(errs, o.err)
			continue
		}
		if o.apply != nil {
			out = append(out, o.apply)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, op, "handler options")
	}
	return out, nil
}

// Error is the typed error Serve, NewHandler, LoadServeOptions and Smoke return
// along their wrap chain. Read it with errors.As, or its class with CodeOf.
type Error = derrors.Error

// Code is a stable machine-readable error class.
type Code = derrors.Code

// Error classes a host can branch on.
const (
	CodeInvalid            = derrors.CodeInvalid
	CodeNotFound           = derrors.CodeNotFound
	CodeUnauthorized       = derrors.CodeUnauthorized
	CodeTimeout            = derrors.CodeTimeout
	CodeCanceled           = derrors.CodeCanceled
	CodeFailedPrecondition = derrors.CodeFailedPrecondition
	CodeUnavailable        = derrors.CodeUnavailable
	CodeRateLimited        = derrors.CodeRateLimited
	CodeNotReady           = derrors.CodeNotReady
	CodeInternal           = derrors.CodeInternal
	CodeConflict           = derrors.CodeConflict
	CodeUnimplemented      = derrors.CodeUnimplemented
)

// CodeOf returns the outermost Code in err's chain, or CodeInternal when err holds
// no *Error.
func CodeOf(err error) Code { return derrors.CodeOf(err) }

// SmokeOptions configures multi-channel L1 smoke against a running gateway. Like
// ServeOptions, its exported fields are pinned by TestFacadeFieldSetsArePinned.
type SmokeOptions = smoke.Options

// SmokeResult is one channel probe outcome. Its exported fields are pinned by
// TestFacadeFieldSetsArePinned.
type SmokeResult = smoke.Result

// Default cheap Cloudflare models (re-exported for hosts/CI).
const (
	DefaultChatModel      = smoke.DefaultChatModel
	DefaultTTSModel       = smoke.DefaultTTSModel
	DefaultSTTModel       = smoke.DefaultSTTModel
	DefaultSystemOneModel = smoke.DefaultSystemOneModel
	DefaultBatchModel     = smoke.DefaultBatchModel
)

// LoadServeOptions resolves host, port, the callback base URL and the MLX backend
// from the environment. BackendURL stays empty unless an MLX variable is set, so
// the config's mlx_local base_url stands. A set but invalid POLYPUS_PORT or
// POLYPUS_MLX_PORT is an error.
func LoadServeOptions() (ServeOptions, error) {
	return config.LoadServeOptions()
}

// Serve starts the Polypus gateway and blocks until ctx is done (then shuts down
// gracefully) or the server fails. The host owns signals: pass a ctx from
// signal.NotifyContext to stop on SIGINT or SIGTERM.
func Serve(ctx context.Context, opts ServeOptions, options ...HandlerOption) error {
	gopts, err := gatewayOptions("polypus.Serve", options)
	if err != nil {
		return err
	}
	return gateway.ListenAndServe(ctx, opts, gopts...)
}

// Handler is the Polypus HTTP handler NewHandler builds. It owns a router with
// worker goroutines and pooled upstream connections, which Close releases. Call
// Close once the server that serves the handler has shut down, or when the host
// replaces the handler (for example on a config reload); requests still in flight
// when Close runs may fail. Close is safe to call more than once.
type Handler interface {
	http.Handler
	io.Closer
}

// NewHandler builds the public HTTP handler without listening. The host serves it
// on its own server and calls Close when done; Serve manages that itself.
func NewHandler(opts ServeOptions, options ...HandlerOption) (Handler, error) {
	gopts, err := gatewayOptions("polypus.NewHandler", options)
	if err != nil {
		return nil, err
	}
	h, err := gateway.NewHandler(opts, gopts...)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// Smoke runs chat, TTS, STT, systemone, and/or batch probes against a gateway.
// Batch is opt-in via Options.Channels (not included in AllSmokeChannels).
func Smoke(ctx context.Context, opts SmokeOptions) ([]SmokeResult, error) {
	return smoke.Run(ctx, opts)
}

// AllSmokeChannels returns the default CI channel set.
func AllSmokeChannels() []string {
	return smoke.AllChannels()
}
