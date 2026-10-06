package gateway

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// RegistryProvider exposes the routing table.
type RegistryProvider interface {
	Registry() *router.Registry
}

// ChatRouter is the Bifrost/OpenAI chat and embed dial surface (≤4 methods).
type ChatRouter interface {
	UsesBifrost(backendID string) bool
	ChatCompletionRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) ([]byte, error)
	ChatCompletionStreamRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) (<-chan []byte, <-chan error, error)
	EmbeddingRaw(ctx context.Context, backendID, model string, body []byte, timeout time.Duration) ([]byte, error)
}

// SpeechRouter is the TTS/STT dial surface (≤2 methods).
type SpeechRouter interface {
	Synthesize(ctx context.Context, req router.SpeechRequest) ([]byte, error)
	Transcribe(ctx context.Context, req router.TranscriptionRequest) ([]byte, string, error)
}

// Router is the composed gateway dial dependency (Registry + Chat + Speech).
// Prefer depending on the smaller interfaces at call sites; *router.Client implements all.
type Router interface {
	RegistryProvider
	ChatRouter
	SpeechRouter
}

// routerCloser is optional; owned Bifrost clients implement Close.
type routerCloser interface {
	Close()
}

// CloudflareClientGet constructs extension clients (defaults to cloudflare.GetClient).
type CloudflareClientGet func(config.BackendDef) (*cloudflare.Client, error)

// HandlerOption configures NewHandler.
type HandlerOption func(*handlerOptions)

type handlerOptions struct {
	router        Router
	cfGet         CloudflareClientGet
	overlayPath   string
	adminKeysPath string
	clock         func() time.Time
}

// WithRouter injects a Router so tests can avoid bifrost.Init.
// Panics if r is nil.
func WithRouter(r Router) HandlerOption {
	if r == nil {
		panic("gateway: WithRouter(nil)")
	}
	return func(o *handlerOptions) {
		o.router = r
	}
}

// WithCloudflareClientGet injects CF client construction for health/inventory.
func WithCloudflareClientGet(fn CloudflareClientGet) HandlerOption {
	return func(o *handlerOptions) {
		o.cfGet = fn
	}
}

// WithAdminStateDir sets overlay and admin key paths under dir (tests).
func WithAdminStateDir(dir string) HandlerOption {
	if strings.TrimSpace(dir) == "" {
		panic("gateway: WithAdminStateDir(empty)")
	}
	return func(o *handlerOptions) {
		o.overlayPath = filepath.Join(dir, "models-allow-overlay.yaml")
		o.adminKeysPath = filepath.Join(dir, "admin-api-keys.json")
	}
}

var _ Router = (*router.Client)(nil)
