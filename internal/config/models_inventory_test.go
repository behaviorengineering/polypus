package config

import (
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestLoadModelsInventoryDefaults(t *testing.T) {
	for _, k := range []string{"POLYPUS_MODELS_WARMUP", "POLYPUS_MODELS_REFRESH_INTERVAL", "POLYPUS_MODELS_CACHE_TTL"} {
		t.Setenv(k, "")
	}
	m, err := LoadModelsInventory()
	if err != nil || !m.Warmup || m.RefreshInterval != DefaultModelsRefreshInterval || m.CacheTTL != DefaultModelsCacheTTL {
		t.Fatalf("defaults: %+v %v", m, err)
	}
}

func TestLoadModelsInventoryOffWords(t *testing.T) {
	for _, v := range []string{"off", "0", "false"} {
		t.Setenv("POLYPUS_MODELS_REFRESH_INTERVAL", v)
		m, err := LoadModelsInventory()
		if err != nil || m.RefreshInterval != 0 || m.RefreshEnabled() {
			t.Fatalf("%q: %+v %v", v, m, err)
		}
	}
	t.Setenv("POLYPUS_MODELS_WARMUP", "off")
	t.Setenv("POLYPUS_MODELS_REFRESH_INTERVAL", "30m")
	if m, err := LoadModelsInventory(); err != nil || m.RefreshEnabled() {
		t.Fatalf("warmup off turns the refresh off: %+v %v", m, err)
	}
}

func TestLoadModelsInventoryRejectsBadValues(t *testing.T) {
	t.Setenv("POLYPUS_MODELS_REFRESH_INTERVAL", "soon")
	t.Setenv("POLYPUS_MODELS_CACHE_TTL", "off")
	m, err := LoadModelsInventory()
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("want invalid, got %v", err)
	}
	if m.CacheTTL != DefaultModelsCacheTTL || m.RefreshInterval != DefaultModelsRefreshInterval {
		t.Fatalf("bad values keep the defaults alongside the error: %+v", m)
	}
	t.Setenv("POLYPUS_MODELS_CACHE_TTL", "5m")
	t.Setenv("POLYPUS_MODELS_REFRESH_INTERVAL", "10m")
	if m, err := LoadModelsInventory(); err != nil || m.CacheTTL != 5*time.Minute || m.RefreshInterval != 10*time.Minute {
		t.Fatalf("%+v %v", m, err)
	}
}
