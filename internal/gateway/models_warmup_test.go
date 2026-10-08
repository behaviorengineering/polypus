package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestModelsWarmupTimeout(t *testing.T) {
	small := config.RouterConfig{Backends: map[string]config.BackendDef{"a": {ID: "a"}}}
	if modelsWarmupTimeout(small) != 15*time.Second {
		t.Fatalf("min: got %v", modelsWarmupTimeout(small))
	}
	big := config.RouterConfig{Backends: make(map[string]config.BackendDef)}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("b%d", i)
		big.Backends[id] = config.BackendDef{ID: id}
	}
	if modelsWarmupTimeout(big) != 60*time.Second {
		t.Fatalf("max: got %v", modelsWarmupTimeout(big))
	}
}

func TestModelsWarmupEnabled(t *testing.T) {
	t.Setenv("POLYPUS_MODELS_WARMUP", "off")
	if modelsWarmupEnabled() {
		t.Fatal("expected off")
	}
	t.Setenv("POLYPUS_MODELS_WARMUP", "")
	if !modelsWarmupEnabled() {
		t.Fatal("expected default on")
	}
}
