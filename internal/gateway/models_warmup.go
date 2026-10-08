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

func modelsWarmupEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POLYPUS_MODELS_WARMUP"))) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
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
