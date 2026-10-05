package router

import (
	"fmt"
	"sync"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestAllowModelAppends(t *testing.T) {
	reg, err := NewRegistry(gatedTestRouterConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AllowModel("cf", "@cf/new"); err != nil {
		t.Fatal(err)
	}
	b, _ := reg.Backend("cf")
	if !b.IsModelAllowed("@cf/new") {
		t.Fatal("expected allowed")
	}
	if err := reg.AllowModel("cf", "@cf/new"); err != nil {
		t.Fatal(err)
	}
}

func TestAllowModelUnknownBackend(t *testing.T) {
	reg, err := NewRegistry(gatedTestRouterConfig())
	if err != nil {
		t.Fatal(err)
	}
	err = reg.AllowModel("nope", "@cf/x")
	if derrors.CodeOf(err) != derrors.CodeNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestAllowModelConcurrent(t *testing.T) {
	reg, err := NewRegistry(gatedTestRouterConfig())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = reg.AllowModel("cf", fmt.Sprintf("@cf/m%d", n))
		}(i)
	}
	wg.Wait()
	_ = reg.Config()
}

func gatedTestRouterConfig() config.RouterConfig {
	return config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"cf": {
				ID:      "cf",
				BaseURL: "http://127.0.0.1:9/v1",
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"@cf/a"},
				},
			},
		},
	}
}
