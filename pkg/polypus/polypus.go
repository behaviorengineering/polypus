// Package polypus is the public product façade for the Polypus inference gateway.
//
// Import this package from hosts and CI. Implementation and quality runners live
// under internal/; cmd binaries are thin wrappers over this API.
package polypus

import (
	"context"
	"net/http"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway"
	"github.com/behaviorengineering/polypus/internal/smoke"
)

// ServeOptions is the gateway listen configuration.
type ServeOptions = config.ServeOptions

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
)

// LoadServeOptions resolves host/port/backend from the environment.
func LoadServeOptions() ServeOptions {
	return config.LoadServeOptions()
}

// Serve starts the Polypus gateway (blocks until the server exits).
func Serve(opts ServeOptions) error {
	return gateway.ListenAndServe(opts)
}

// ListenAndServe is an alias for Serve.
func ListenAndServe(opts ServeOptions) error {
	return Serve(opts)
}

// NewHandler builds the public HTTP handler without listening.
func NewHandler(opts ServeOptions, options ...gateway.HandlerOption) (http.Handler, error) {
	return gateway.NewHandler(opts, options...)
}

// Smoke runs chat, TTS, STT, and/or systemone probes against a gateway.
func Smoke(ctx context.Context, opts SmokeOptions) ([]SmokeResult, error) {
	return smoke.Run(ctx, opts)
}

// SmokeCLI runs the polypus-smoke command surface (flags + JSONL stdout).
func SmokeCLI(args []string) int {
	return smoke.RunCLI(args)
}

// AllSmokeChannels returns the default CI channel set.
func AllSmokeChannels() []string {
	return smoke.AllChannels()
}
