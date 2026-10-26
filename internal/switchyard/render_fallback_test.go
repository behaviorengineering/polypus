package switchyard

import (
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestRenderFallbackRoute(t *testing.T) {
	cfg := config.RouterConfig{
		Routers: map[string]config.NamedRouter{
			"turno": {
				Name:       "turno",
				Capability: config.CapChat,
				Route: config.RouterRoute{
					Type: config.RouteFallback,
					FallbackTargets: []string{
						"claude_team/claude-sonnet-5-5",
						"openrouter/anthropic/claude-sonnet-5.5",
					},
				},
			},
		},
	}
	out, err := Render(cfg, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	body := string(out)
	for _, want := range []string{
		"[llm_clients.polypus_fallback]\nformat = \"openai_chat\"\nbase_url = \"http://127.0.0.1:1320/v1\"\nmax_retries = 0\n",
		"[targets.turno_0]\nid = \"claude_team/claude-sonnet-5-5\"\nllm_client = \"polypus_fallback\"\n",
		"[targets.turno_1]\nid = \"openrouter/anthropic/claude-sonnet-5.5\"\nllm_client = \"polypus_fallback\"\n",
		"[routes.turno]\nid = \"router/turno\"\ntype = \"random\"\ntargets = [\"turno_0\", \"turno_1\"]\nweights = [1, 0]\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
}

func TestRenderWithoutFallbackOmitsNoRetryClient(t *testing.T) {
	cfg := config.RouterConfig{
		Routers: map[string]config.NamedRouter{
			"scribe": {
				Name:       "scribe",
				Capability: config.CapChat,
				Route:      config.RouterRoute{Type: config.RoutePassthrough, Target: "cf_local/m"},
			},
		},
	}
	out, err := Render(cfg, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "polypus_fallback") {
		t.Fatalf("no fallback route, no fallback client:\n%s", out)
	}
}
