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
	// Warmup has no caller, so no inbound credential reaches local backends.
	models := h.collectModelsWith(ctx, "", asInventory, h.refreshBackend)
	h.log.Info("polypus: models inventory warmed", "inventory_view", asInventory, "count", len(models))
}

// startModelsRefreshLoop runs the refresh loop until refreshCtx is done. wait blocks
// until the loop, and any refresh in flight, has stopped; it is never nil.
// Each refresh is one models pass, which sets its own budget.
func (h modelsHandler) startModelsRefreshLoop(refreshCtx context.Context) (wait func()) {
	if !h.inventory.RefreshEnabled() {
		return func() {}
	}
	interval := h.inventory.RefreshInterval
	warm := func(iterCtx context.Context) { h.warmInventory(iterCtx, true) }
	var wg sync.WaitGroup
	wg.Go(func() { runModelsRefreshLoop(refreshCtx, interval, nil, warm) })
	return wg.Wait
}
