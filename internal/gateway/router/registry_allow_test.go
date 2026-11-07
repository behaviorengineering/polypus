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

func TestAllowModelKeepsSystemOneAllow(t *testing.T) {
	cfg := gatedTestRouterConfig()
	b := cfg.Backends["cf"]
	b.Models.SystemOneAllowConfigured = true
	b.Models.SystemOneAllow = []string{"typesafe/jev", "perplexity/pplx-decider-v1.2"}
	cfg.Backends["cf"] = b
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.AllowModel("cf", "@cf/new"); err != nil {
		t.Fatal(err)
	}
	got := reg.Config().Backends["cf"].Models
	if got == nil {
		t.Fatal("models missing")
	}
	if !got.SystemOneAllowConfigured {
		t.Fatal("SystemOneAllowConfigured dropped")
	}
	if len(got.SystemOneAllow) != 2 || got.SystemOneAllow[0] != "typesafe/jev" || got.SystemOneAllow[1] != "perplexity/pplx-decider-v1.2" {
		t.Fatalf("SystemOneAllow dropped: %v", got.SystemOneAllow)
	}
	b2, _ := reg.Backend("cf")
	if !b2.IsModelAllowed("@cf/new") {
		t.Fatal("expected chat model allowed")
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
			if err := reg.AllowModel("cf", fmt.Sprintf("@cf/m%d", n)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	allow := reg.Config().Backends["cf"].Models.Allow
	if len(allow) != 9 {
		t.Fatalf("every concurrent AllowModel must land: got %d entries %v", len(allow), allow)
	}
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

func TestWithAllowedModelCopiesEveryFieldAndOwnsItsSlices(t *testing.T) {
	cfg := gatedTestRouterConfig()
	b := cfg.Backends["cf"]
	syncOff := false
	b.Models.Sync = &syncOff
	b.Models.SystemOneAllow = []string{"typesafe/jev"}
	cfg.Backends["cf"] = b
	snap, changed, err := withAllowedModel(cfg, "cf", "@cf/new")
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got := snap.Backends["cf"].Models
	if got.Sync != b.Models.Sync || !got.AllowConfigured {
		t.Fatalf("fields dropped: %+v", got)
	}
	got.SystemOneAllow[0] = "changed"
	if b.Models.SystemOneAllow[0] != "typesafe/jev" {
		t.Fatal("the new snapshot must not share SystemOneAllow with the old one")
	}
	if len(b.Models.Allow) == len(got.Allow) {
		t.Fatal("the old snapshot's allow list must stay as it was")
	}
}
