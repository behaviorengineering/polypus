package gateway

import (
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestRouterCatalogModelsSorted(t *testing.T) {
	cfg := config.RouterConfig{
		Routers: map[string]config.NamedRouter{
			"zebra": {Name: "zebra"},
			"alpha": {Name: "alpha"},
		},
	}
	got := routerCatalogModels(cfg)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].ID != "router/alpha" || got[1].ID != "router/zebra" {
		t.Fatalf("order: %#v", got)
	}
	if got[0].OwnedBy != "polypus" {
		t.Fatalf("owned_by: %q", got[0].OwnedBy)
	}
}

func TestMergeBackendInventoriesAllowList(t *testing.T) {
	cfg := config.RouterConfig{
		DefaultChatBackend: "leaf",
		Backends: map[string]config.BackendDef{
			"leaf": {
				ID: "leaf",
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"leaf/ok"},
				},
			},
		},
	}
	results := []backendInventory{{
		backend: "leaf",
		models: []openaiModel{
			{ID: "leaf/ok", Object: "model"},
			{ID: "leaf/blocked", Object: "model"},
		},
	}}

	tool := mergeBackendInventories(cfg, false, results)
	if _, ok := tool["leaf/ok"]; !ok {
		t.Fatal("expected leaf/ok")
	}
	if _, ok := tool["leaf/blocked"]; ok {
		t.Fatal("blocked model should be filtered")
	}
	if _, ok := tool["ok"]; !ok {
		t.Fatal("expected bare default-backend alias ok")
	}

	inv := mergeBackendInventories(cfg, true, results)
	if _, ok := inv["leaf/blocked"]; !ok {
		t.Fatal("inventory view keeps blocked models")
	}
}

func TestMergeSeedModelsSkipsDisallowed(t *testing.T) {
	cfg := config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"mlx": {
				ID: "mlx",
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"mlx/tts-a"},
				},
			},
		},
	}
	byID := map[string]openaiModel{}
	seeds := []openaiModel{
		{ID: "mlx/tts-a", OwnedBy: "mlx"},
		{ID: "mlx/tts-b", OwnedBy: "mlx"},
	}
	mergeSeedModels(cfg, false, []string{"mlx"}, byID, seeds)
	if _, ok := byID["mlx/tts-a"]; !ok {
		t.Fatal("expected allowed seed")
	}
	if _, ok := byID["mlx/tts-b"]; ok {
		t.Fatal("disallowed seed should be skipped")
	}
}

func TestFinalizeModelListSorts(t *testing.T) {
	out := finalizeModelList(
		[]openaiModel{{ID: "router/z"}},
		map[string]openaiModel{"b": {ID: "b"}, "a": {ID: "a"}},
	)
	want := []string{"a", "b", "router/z"}
	if len(out) != 3 {
		t.Fatalf("len=%d", len(out))
	}
	for i, id := range want {
		if out[i].ID != id {
			t.Fatalf("index %d: got %q want %q", i, out[i].ID, id)
		}
	}
}

func TestValidateNamedRouterForChat(t *testing.T) {
	cases := []struct {
		name    string
		nr      config.NamedRouter
		vision  bool
		wantSub string
	}{
		{
			name: "ok",
			nr:   config.NamedRouter{Capability: config.CapChat},
		},
		{
			name:    "not chat",
			nr:      config.NamedRouter{Capability: config.CapTTS},
			wantSub: "does not support chat",
		},
		{
			name:    "vision",
			nr:      config.NamedRouter{Capability: config.CapChat},
			vision:  true,
			wantSub: "does not support vision",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateNamedRouterForChat(tc.nr, "router/x", tc.vision)
			if tc.wantSub == "" {
				if msg != "" {
					t.Fatalf("msg=%q", msg)
				}
				return
			}
			if !strings.Contains(msg, tc.wantSub) {
				t.Fatalf("msg=%q want substring %q", msg, tc.wantSub)
			}
		})
	}
}

func TestClassifyNamedRouterRoute(t *testing.T) {
	if classifyNamedRouterRoute(config.RoutePassthrough) != dispatchPassthrough {
		t.Fatal("passthrough")
	}
	if classifyNamedRouterRoute(config.RouteStageRouter) != dispatchSwitchyard {
		t.Fatal("stage_router")
	}
	if classifyNamedRouterRoute(config.RouteLLMClassifier) != dispatchSwitchyard {
		t.Fatal("llm_classifier")
	}
	if classifyNamedRouterRoute("nope") != dispatchUnknown {
		t.Fatal("unknown")
	}
}
