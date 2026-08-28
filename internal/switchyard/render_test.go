package switchyard

import (
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestRenderGoldenStageRouter(t *testing.T) {
	window := 3
	cfg := config.RouterConfig{
		Routers: map[string]config.NamedRouter{
			"investigator": {
				Name:       "investigator",
				Capability: config.CapChat,
				Route: config.RouterRoute{
					Type:                config.RouteStageRouter,
					Capable:             "cf_local/@cf/zai-org/glm-4.7-flash",
					Efficient:           "lm_studio/qwen2.5-7b",
					Picker:              config.PickerEfficientFirst,
					ConfidenceThreshold: 0.5,
					RecentTurnWindow:    &window,
				},
			},
			"scribe": {
				Name:       "scribe",
				Capability: config.CapChat,
				Route: config.RouterRoute{
					Type:   config.RoutePassthrough,
					Target: "cf_local/@cf/google/gemma-4-26b-a4b-it",
				},
			},
		},
	}
	out, err := Render(cfg, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if !strings.Contains(body, `schema_version = 1`) {
		t.Fatalf("missing schema: %s", body)
	}
	if !strings.Contains(body, `base_url = "http://127.0.0.1:1320/v1"`) {
		t.Fatalf("missing polypus client: %s", body)
	}
	if !strings.Contains(body, `[routes.investigator]`) {
		t.Fatalf("missing route: %s", body)
	}
	if !strings.Contains(body, `id = "router/investigator"`) {
		t.Fatalf("missing public id: %s", body)
	}
	if !strings.Contains(body, `[targets.investigator_capable]`) || !strings.Contains(body, `[targets.investigator_efficient]`) {
		t.Fatalf("missing targets: %s", body)
	}
	if strings.Contains(body, "scribe") {
		t.Fatalf("passthrough must be omitted: %s", body)
	}
}

func TestRenderEmptyRouters(t *testing.T) {
	out, err := Render(config.RouterConfig{}, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	if !strings.Contains(body, `[llm_clients.polypus]`) {
		t.Fatalf("expected polypus client: %s", body)
	}
	if strings.Contains(body, `[routes.`) {
		t.Fatalf("unexpected routes: %s", body)
	}
}
