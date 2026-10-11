package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

const defaultModelsRefreshInterval = 30 * time.Minute

func modelsWarmupEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POLYPUS_MODELS_WARMUP"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func modelsRefreshInterval() time.Duration {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POLYPUS_MODELS_REFRESH_INTERVAL"))) {
	case "0", "false", "no", "off":
		return 0
	case "":
		return defaultModelsRefreshInterval
	default:
		d, err := time.ParseDuration(strings.TrimSpace(os.Getenv("POLYPUS_MODELS_REFRESH_INTERVAL")))
		if err != nil || d <= 0 {
			return defaultModelsRefreshInterval
		}
		return d
	}
}

func modelsRefreshEnabled() bool {
	if !modelsWarmupEnabled() {
		return false
	}
	return modelsRefreshInterval() > 0
}

func modelsWarmupTimeout(cfg config.RouterConfig) time.Duration {
	n := len(cfg.BackendIDs())
	if n < 1 {
		n = 1
	}
	d := time.Duration(n) * modelsListTimeout
	if d < 15*time.Second {
		return 15 * time.Second
	}
	if d > 60*time.Second {
		return 60 * time.Second
	}
	return d
}

type refreshTickerFactory func(time.Duration) (<-chan time.Time, func())

func defaultRefreshTicker(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

type modelsInventoryWarmFn func(context.Context)

// runModelsRefreshLoop re-fetches backend inventories until ctx is canceled.
func runModelsRefreshLoop(ctx context.Context, interval time.Duration, newTicker refreshTickerFactory, warm modelsInventoryWarmFn) {
	if interval <= 0 || warm == nil {
		return
	}
	if newTicker == nil {
		newTicker = defaultRefreshTicker
	}
	tick, stop := newTicker(interval)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			warm(ctx)
		}
	}
}

// warmModelsInventory prefetches enabled model catalogs (same path as GET /v1/models).
func (h modelsHandler) warmModelsInventory(ctx context.Context) {
	if h.shared == nil || h.router == nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		slog.Warn("polypus: models warmup", "err", err)
		return
	}
	models := h.collectModels(req, false)
	slog.Info("polypus: models inventory warmup complete", "enabled_count", len(models))
}

func (h modelsHandler) warmModelsInventoryInventoryView(ctx context.Context) {
	if h.shared == nil || h.router == nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/", nil)
	if err != nil {
		slog.Warn("polypus: models inventory refresh", "err", err)
		return
	}
	models := h.collectModels(req, true)
	slog.Info("polypus: models inventory refresh complete", "inventory_count", len(models))
}

func (h modelsHandler) startModelsRefreshLoop(refreshCtx context.Context, cfg config.RouterConfig) {
	if !modelsRefreshEnabled() {
		return
	}
	interval := modelsRefreshInterval()
	warm := func(iterCtx context.Context) {
		timeout := modelsWarmupTimeout(cfg)
		runCtx, cancel := context.WithTimeout(iterCtx, timeout)
		defer cancel()
		h.warmModelsInventoryInventoryView(runCtx)
	}
	go runModelsRefreshLoop(refreshCtx, interval, nil, warm)
}
