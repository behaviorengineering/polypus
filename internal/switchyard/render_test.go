package switchyard

import (
	"reflect"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/pelletier/go-toml/v2"
)

// decode parses rendered routes.toml into plain maps, so the tests check the key
// names Switchyard reads, not the renderer's own structs.
func decode(t *testing.T, out []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := toml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("rendered TOML does not parse: %v\n%s", err, out)
	}
	return doc
}

// at returns the value at a dotted path, failing the test when it is missing.
func at(t *testing.T, doc map[string]any, path string) any {
	t.Helper()
	var cur any = doc
	for _, key := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: %q is not under a table", path, key)
		}
		if cur, ok = m[key]; !ok {
			t.Fatalf("%s: missing %q", path, key)
		}
	}
	return cur
}

func wantAt(t *testing.T, doc map[string]any, path string, want any) {
	t.Helper()
	if got := at(t, doc, path); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", path, got, want)
	}
}

func hasKey(doc map[string]any, table, key string) bool {
	m, _ := doc[table].(map[string]any)
	_, ok := m[key]
	return ok
}

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
	doc := decode(t, out)
	wantAt(t, doc, "schema_version", int64(1))
	wantAt(t, doc, "llm_clients.polypus.base_url", "http://127.0.0.1:1320/v1")
	wantAt(t, doc, "routes.investigator.id", "router/investigator")
	wantAt(t, doc, "routes.investigator.type", "stage_router")
	wantAt(t, doc, "routes.investigator.capable_target", "investigator_capable")
	wantAt(t, doc, "routes.investigator.efficient_target", "investigator_efficient")
	wantAt(t, doc, "routes.investigator.picker", string(config.PickerEfficientFirst))
	wantAt(t, doc, "routes.investigator.confidence_threshold", 0.5)
	wantAt(t, doc, "routes.investigator.recent_turn_window", int64(3))
	wantAt(t, doc, "targets.investigator_capable.id", "cf_local/@cf/zai-org/glm-4.7-flash")
	wantAt(t, doc, "targets.investigator_efficient.llm_client", "polypus")
	if hasKey(doc, "routes", "scribe") {
		t.Fatal("passthrough must be omitted")
	}
}

func TestRenderEmptyRouters(t *testing.T) {
	out, err := Render(config.RouterConfig{}, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, out)
	wantAt(t, doc, "llm_clients.polypus.format", "openai_chat")
	if _, ok := doc["routes"]; ok {
		t.Fatalf("unexpected routes:\n%s", out)
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
	doc := decode(t, out)
	wantAt(t, doc, "targets.smart_classifier.id", "cf_local/@cf/zai-org/glm-4.7-flash")
	wantAt(t, doc, "targets.fast.id", "lm_studio/ornith-1.0-9b")
	wantAt(t, doc, "targets.premium.llm_client", "polypus")
	wantAt(t, doc, "routes.smart.id", "router/smart")
	wantAt(t, doc, "routes.smart.type", "llm_classifier")
	wantAt(t, doc, "routes.smart.mode", "custom")
	wantAt(t, doc, "routes.smart.classifier_target", "smart_classifier")
	wantAt(t, doc, "routes.smart.targets", []any{"fast", "premium"})
	wantAt(t, doc, "routes.smart.default_target", "premium")
	wantAt(t, doc, "routes.smart.policy.type", "target_selector")
	wantAt(t, doc, "routes.smart.policy.selector", "/decision/target")
	wantAt(t, doc, "routes.smart.session_affinity", true)
	wantAt(t, doc, "routes.smart.prompt", "Choose the best configured target for this request.")
	if hasKey(doc, "routes", "scribe") {
		t.Fatal("passthrough must be omitted")
	}
}

// TestRenderQuotesAwkwardStrings covers what hand quoting got wrong: quotes, triple
// quotes, backslashes, control characters, astral runes and a trailing quote must
// all survive the round trip.
func TestRenderQuotesAwkwardStrings(t *testing.T) {
	prompt := "line one\nsay \"hi\" and ''' then \\ and \a bell and \U0001F600 and a trailing quote'"
	cfg := config.RouterConfig{Routers: map[string]config.NamedRouter{
		"odd": {Name: "odd", Capability: config.CapChat, Route: config.RouterRoute{
			Type: config.RouteLLMClassifier, Mode: config.ClassifierModeCustom,
			Classifier: "a/b", DefaultTarget: "x", Prompt: prompt, ResponseSchema: `{"k":"v"}`,
			PolicyType: config.PolicyTargetSelector, PolicySelector: "/t",
			Targets: []config.ClassifierTarget{{Name: "x", Model: `a/"quoted"`}},
		}},
	}}
	out, err := Render(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	doc := decode(t, out)
	wantAt(t, doc, "routes.odd.prompt", prompt)
	wantAt(t, doc, "routes.odd.response_schema", `{"k":"v"}`)
	wantAt(t, doc, "targets.x.id", `a/"quoted"`)
}
