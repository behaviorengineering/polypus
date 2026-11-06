package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

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

// warmInventory refreshes every backend's model catalog live into the cache that
// GET /v1/models serves: the enabled models at startup, or the full inventory view
// on refresh.
func (h modelsHandler) warmInventory(ctx context.Context, asInventory bool) {
	if h.shared == nil || h.registry == nil {
		return
	}
	// Warmup has no caller, so no inbound credential reaches local backends.
	models := h.collectModelsWith(ctx, "", asInventory, h.refreshBackend)
	h.log.Info("polypus: models inventory warmed", "inventory_view", asInventory, "count", len(models))
}

// startModelsRefreshLoop runs the refresh loop until refreshCtx is done. wait blocks
// until the loop, and any refresh in flight, has stopped; it is never nil.
func (h modelsHandler) startModelsRefreshLoop(refreshCtx context.Context, cfg config.RouterConfig) (wait func()) {
	if !h.models.RefreshEnabled() {
		return func() {}
	}
	interval := h.models.RefreshInterval
	warm := func(iterCtx context.Context) {
		timeout := modelsWarmupTimeout(cfg)
		runCtx, cancel := context.WithTimeout(iterCtx, timeout)
		defer cancel()
		h.warmInventory(runCtx, true)
	}
	var wg sync.WaitGroup
	wg.Go(func() { runModelsRefreshLoop(refreshCtx, interval, nil, warm) })
	return wg.Wait
}
