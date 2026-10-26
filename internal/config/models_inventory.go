package config

import (
	"errors"
	"os"
	"strings"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// Defaults for the models inventory.
const (
	DefaultModelsRefreshInterval = 30 * time.Minute
	DefaultModelsCacheTTL        = time.Hour
)

// ModelsInventory is how the gateway keeps its model catalog warm, from
// POLYPUS_MODELS_WARMUP, POLYPUS_MODELS_REFRESH_INTERVAL and POLYPUS_MODELS_CACHE_TTL.
type ModelsInventory struct {
	// Warmup prefetches catalogs at startup.
	Warmup bool
	// RefreshInterval re-fetches the inventory view; zero turns the refresh off.
	RefreshInterval time.Duration
	// CacheTTL is how long a fetched catalog is served before it is fetched again.
	CacheTTL time.Duration
}

// RefreshEnabled reports whether the periodic refresh runs (it needs warmup on).
func (m ModelsInventory) RefreshEnabled() bool {
	return m.Warmup && m.RefreshInterval > 0
}

// LoadModelsInventory reads the models inventory settings once. A value that does
// not parse is an error naming the variable, not a silent default.
func LoadModelsInventory() (ModelsInventory, error) {
	warmup, warmErr := ParseEnvBool("POLYPUS_MODELS_WARMUP", true)
	refresh, refreshErr := envDurationOrOff("POLYPUS_MODELS_REFRESH_INTERVAL", DefaultModelsRefreshInterval)
	ttl, ttlErr := envDurationOrOff("POLYPUS_MODELS_CACHE_TTL", DefaultModelsCacheTTL)
	if ttl == 0 {
		ttlErr = errors.Join(ttlErr, derrors.New(derrors.CodeInvalid, "config.LoadModelsInventory", "cache TTL cannot be off").
			With("env", "POLYPUS_MODELS_CACHE_TTL"))
		ttl = DefaultModelsCacheTTL
	}
	out := ModelsInventory{Warmup: warmup, RefreshInterval: refresh, CacheTTL: ttl}
	if err := errors.Join(warmErr, refreshErr, ttlErr); err != nil {
		return out, derrors.Wrap(err, derrors.CodeInvalid, "config.LoadModelsInventory", "models inventory settings")
	}
	return out, nil
}

// envDurationOrOff reads a positive duration: fallback when unset, zero for a false
// word (0, false, no, off), and an error for anything else that does not parse.
func envDurationOrOff(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	if on, ok := ParseBoolishEnv(raw); ok && !on {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback, derrors.New(derrors.CodeInvalid, "config.envDurationOrOff", "not a positive duration or off").
			With("env", key).With("value", raw)
	}
	return d, nil
}
