// Package polypus is the public product façade for the Polypus inference gateway.
//
// Import this package from hosts and CI. Implementation and quality runners live
// under internal/; cmd binaries are thin wrappers over this API.
package polypus

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway"
	"github.com/behaviorengineering/polypus/internal/smoke"
)

// ServeOptions is the gateway listen configuration.
type ServeOptions = config.ServeOptions

// HandlerOption configures Serve and NewHandler. Build one with the With* functions
// below.
type HandlerOption = gateway.HandlerOption

// WithLogger sets the logger the gateway writes warnings and request failures to
// (slog.Default otherwise). Panics if l is nil.
func WithLogger(l *slog.Logger) HandlerOption { return gateway.WithLogger(l) }

// WithAdminStateDir keeps the models allow overlay and the admin key store under dir
// instead of the XDG state directory. Panics if dir is empty.
func WithAdminStateDir(dir string) HandlerOption { return gateway.WithAdminStateDir(dir) }

// SmokeOptions configures multi-channel L1 smoke against a running gateway.
type SmokeOptions = smoke.Options

// SmokeResult is one channel probe outcome.
type SmokeResult = smoke.Result

// Default cheap Cloudflare models (re-exported for hosts/CI).
const (
	DefaultChatModel      = smoke.DefaultChatModel
	DefaultTTSModel       = smoke.DefaultTTSModel
	DefaultSTTModel       = smoke.DefaultSTTModel
	DefaultSystemOneModel = smoke.DefaultSystemOneModel
	DefaultBatchModel     = smoke.DefaultBatchModel
)

// LoadServeOptions resolves host/port/backend from the environment. A set but
// invalid POLYPUS_PORT or POLYPUS_MLX_PORT is an error.
func LoadServeOptions() (ServeOptions, error) {
	return config.LoadServeOptions()
}

// Serve starts the Polypus gateway (blocks until the server exits).
func Serve(opts ServeOptions, options ...HandlerOption) error {
	return gateway.ListenAndServe(opts, options...)
}

// ListenAndServe is an alias for Serve.
func ListenAndServe(opts ServeOptions, options ...HandlerOption) error {
	return Serve(opts, options...)
}

// NewHandler builds the public HTTP handler without listening.
func NewHandler(opts ServeOptions, options ...HandlerOption) (http.Handler, error) {
	return gateway.NewHandler(opts, options...)
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
