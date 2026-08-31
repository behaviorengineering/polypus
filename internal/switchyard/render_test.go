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

func TestRenderLLMClassifierCustom(t *testing.T) {
	affinity := true
	cfg := config.RouterConfig{
		Routers: map[string]config.NamedRouter{
			"smart": {
				Name:       "smart",
				Capability: config.CapChat,
				Route: config.RouterRoute{
					Type:            config.RouteLLMClassifier,
					Mode:            config.ClassifierModeCustom,
					Classifier:      "cf_local/@cf/zai-org/glm-4.7-flash",
					DefaultTarget:   "premium",
					Prompt:          "Choose the best configured target for this request.",
					ResponseSchema:  `{"type":"object","properties":{"decision":{"type":"object","properties":{"target":{"type":"string","enum":["fast","premium"]}},"required":["target"]}},"required":["decision"]}`,
					PolicyType:      config.PolicyTargetSelector,
					PolicySelector:  "/decision/target",
					SessionAffinity: &affinity,
					Targets: []config.ClassifierTarget{
						{Name: "fast", Model: "lm_studio/ornith-1.0-9b"},
						{Name: "premium", Model: "cf_local/@cf/google/gemma-4-26b-a4b-it"},
					},
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
	if !strings.Contains(body, `[targets.smart_classifier]`) {
		t.Fatalf("missing classifier target: %s", body)
	}
	if !strings.Contains(body, `id = "cf_local/@cf/zai-org/glm-4.7-flash"`) {
		t.Fatalf("missing classifier leaf: %s", body)
	}
	if !strings.Contains(body, `[targets.fast]`) || !strings.Contains(body, `[targets.premium]`) {
		t.Fatalf("missing leaf targets: %s", body)
	}
	if !strings.Contains(body, `[routes.smart]`) {
		t.Fatalf("missing route: %s", body)
	}
	if !strings.Contains(body, `id = "router/smart"`) {
		t.Fatalf("missing public id: %s", body)
	}
	if !strings.Contains(body, `type = "llm_classifier"`) || !strings.Contains(body, `mode = "custom"`) {
		t.Fatalf("missing classifier type/mode: %s", body)
	}
	if !strings.Contains(body, `classifier_target = "smart_classifier"`) {
		t.Fatalf("missing classifier_target: %s", body)
	}
	if !strings.Contains(body, `targets = ["fast", "premium"]`) {
		t.Fatalf("missing targets list: %s", body)
	}
	if !strings.Contains(body, `default_target = "premium"`) {
		t.Fatalf("missing default_target: %s", body)
	}
	if !strings.Contains(body, `[routes.smart.policy]`) {
		t.Fatalf("missing policy: %s", body)
	}
	if !strings.Contains(body, `type = "target_selector"`) || !strings.Contains(body, `selector = "/decision/target"`) {
		t.Fatalf("missing policy fields: %s", body)
	}
	if !strings.Contains(body, `session_affinity = true`) {
		t.Fatalf("missing session_affinity: %s", body)
	}
	if !strings.Contains(body, "Choose the best configured target") {
		t.Fatalf("missing prompt: %s", body)
	}
	if strings.Contains(body, "scribe") {
		t.Fatalf("passthrough must be omitted: %s", body)
	}
}

func TestTomlMultilineLiteralAndFallback(t *testing.T) {
	lit := tomlMultiline("hello\nworld")
	if !strings.HasPrefix(lit, "'''\n") || !strings.HasSuffix(lit, "\n'''") {
		t.Fatalf("expected literal block: %q", lit)
	}
	withTriple := tomlMultiline("has ''' inside")
	if strings.HasPrefix(withTriple, "'''") {
		t.Fatalf("expected basic string fallback: %q", withTriple)
	}
	if !strings.Contains(withTriple, "'") {
		t.Fatalf("missing quote content: %q", withTriple)
	}
	trailing := tomlMultiline("ends with quote'")
	if strings.HasPrefix(trailing, "'''") {
		t.Fatalf("trailing quote must use basic string: %q", trailing)
	}
	quoted := tomlMultiline(`say "hi"`)
	if !strings.HasPrefix(quoted, "'''") && !strings.Contains(quoted, `\"`) {
		t.Fatalf("expected literal or escaped quotes: %q", quoted)
	}
}
