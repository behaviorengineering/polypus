package gateway

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func mixedSystemOneRouterConfig() config.RouterConfig {
	return config.RouterConfig{
		Chat: config.CapabilityBackend{Enabled: true, Default: "cf_local"},
		SystemOne: config.CapabilityBackend{
			Enabled: true,
			Default: "cf_local",
		},
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Capabilities: []config.Capability{config.CapChat, config.CapSystemOne},
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"@cf/a", "typesafe/jev"},
				},
			},
		},
	}
}

func TestIsSystemOneListedModel(t *testing.T) {
	cfg := mixedSystemOneRouterConfig()
	cases := []struct {
		id   string
		want bool
	}{
		{"cf_local/typesafe/jev", true},
		{"typesafe/jev", true},
		{"cf_local/@cf/a", false},
		{"@cf/a", false},
		{"router/investigator", false},
	}
	for _, tc := range cases {
		if got := isSystemOneListedModel(cfg, tc.id); got != tc.want {
			t.Fatalf("id %q: got %v want %v", tc.id, got, tc.want)
		}
	}
}

func TestIsSystemOneListedModelRejectsPrefixWithoutAllow(t *testing.T) {
	cfg := mixedSystemOneRouterConfig()
	if isSystemOneListedModel(cfg, "cf_local/typesafe/foo") {
		t.Fatal("typesafe/foo should not be systemone without systemone_allow")
	}
}

func TestIsSystemOneListedModelExplicitSystemOneAllow(t *testing.T) {
	cfg := mixedSystemOneRouterConfig()
	b := cfg.Backends["cf_local"]
	b.Models.SystemOneAllowConfigured = true
	b.Models.SystemOneAllow = []string{"typesafe/jev", "typesafe/foo"}
	cfg.Backends["cf_local"] = b
	if !isSystemOneListedModel(cfg, "cf_local/typesafe/foo") {
		t.Fatal("expected typesafe/foo when listed in systemone_allow")
	}
}

func TestIsSystemOneListedModelRequiresCapability(t *testing.T) {
	cfg := config.RouterConfig{
		Chat: config.CapabilityBackend{Enabled: true, Default: "leaf"},
		Backends: map[string]config.BackendDef{
			"leaf": {
				ID:           "leaf",
				Capabilities: []config.Capability{config.CapChat},
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"typesafe/jev"},
				},
			},
		},
	}
	if isSystemOneListedModel(cfg, "leaf/typesafe/jev") {
		t.Fatal("chat-only backend should not classify typesafe/jev as systemone")
	}
}

func TestPartitionModels(t *testing.T) {
	cfg := mixedSystemOneRouterConfig()
	all := []openaiModel{
		{ID: "cf_local/@cf/a", Object: "model"},
		{ID: "@cf/a", Object: "model"},
		{ID: "cf_local/typesafe/jev", Object: "model"},
		{ID: "typesafe/jev", Object: "model"},
		{ID: "router/alpha", Object: "model", OwnedBy: "polypus"},
	}
	openai := modelsForSurface(cfg, all, surfaceOpenAI)
	for _, m := range openai {
		if stringsContainsJEV(m.ID) {
			t.Fatalf("openai surface leaked JEV: %q", m.ID)
		}
	}
	if len(openai) != 2 {
		t.Fatalf("openai deduped aliases: %#v", openai)
	}
	if !containsModelID(openai, "cf_local/@cf/a") {
		t.Fatalf("openai missing chat model: %#v", openai)
	}
	if !containsModelID(openai, "router/alpha") {
		t.Fatalf("openai missing router: %#v", openai)
	}
	if containsModelID(openai, "@cf/a") {
		t.Fatalf("openai should drop bare alias: %#v", openai)
	}

	sys := modelsForSurface(cfg, all, surfaceSystemOne)
	if len(sys) != 1 || sys[0].ID != "cf_local/typesafe/jev" {
		t.Fatalf("systemone deduped jev: %#v", sys)
	}
	if containsModelID(sys, "cf_local/@cf/a") {
		t.Fatalf("systemone leaked chat: %#v", sys)
	}
}

func TestModelsForSurfaceDedupeSystemOneDualAllowIDs(t *testing.T) {
	cfg := mixedSystemOneRouterConfig()
	b := cfg.Backends["cf_local"]
	b.Models.SystemOneAllowConfigured = true
	b.Models.SystemOneAllow = []string{"typesafe/jev", "@cf/typesafe/jev"}
	cfg.Backends["cf_local"] = b
	all := []openaiModel{
		{ID: "@cf/typesafe/jev", Object: "model", OwnedBy: "cf_local"},
		{ID: "cf_local/@cf/typesafe/jev", Object: "model", OwnedBy: "cf_local"},
		{ID: "typesafe/jev", Object: "model", OwnedBy: "cf_local"},
		{ID: "cf_local/typesafe/jev", Object: "model", OwnedBy: "cf_local"},
	}
	sys := modelsForSurface(cfg, all, surfaceSystemOne)
	if len(sys) != 2 {
		t.Fatalf("expected two downstream ids, got %#v", sys)
	}
	ids := map[string]bool{}
	for _, m := range sys {
		ids[m.ID] = true
	}
	if !ids["cf_local/typesafe/jev"] || !ids["cf_local/@cf/typesafe/jev"] {
		t.Fatalf("ids: %v", ids)
	}
}

func stringsContainsJEV(id string) bool {
	return id == "typesafe/jev" || id == "cf_local/typesafe/jev" || id == "typesafe/foo"
}

func containsModelID(models []openaiModel, id string) bool {
	for _, m := range models {
		if m.ID == id {
			return true
		}
	}
	return false
}

func TestBuildAPICatalogOmitsSystemOneWhenDisabled(t *testing.T) {
	cfg := config.RouterConfig{
		Chat: config.CapabilityBackend{Enabled: true, Default: "cf_local"},
		Backends: map[string]config.BackendDef{
			"cf_local": {ID: "cf_local", Capabilities: []config.Capability{config.CapChat}},
		},
	}
	cat := buildAPICatalog(cfg)
	if len(cat.APIs) != 1 || cat.APIs[0].ID != "openai" {
		t.Fatalf("expected openai only: %#v", cat.APIs)
	}
	if cat.Object != "api_catalog" {
		t.Fatalf("object: %q", cat.Object)
	}
}

func TestBuildAPICatalogIncludesSystemOneWhenEnabled(t *testing.T) {
	cfg := mixedSystemOneRouterConfig()
	cat := buildAPICatalog(cfg)
	if len(cat.APIs) != 2 {
		t.Fatalf("len apis: %d %#v", len(cat.APIs), cat.APIs)
	}
	if cat.APIs[1].Schema != "/v1/apis/systemone/schema.json" {
		t.Fatalf("schema: %q", cat.APIs[1].Schema)
	}
}
