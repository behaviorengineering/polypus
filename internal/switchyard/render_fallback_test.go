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
	doc := decode(t, out)
	wantAt(t, doc, "llm_clients.polypus_fallback.base_url", "http://127.0.0.1:1320/v1")
	wantAt(t, doc, "llm_clients.polypus_fallback.max_retries", int64(0))
	wantAt(t, doc, "targets.turno_0.id", "claude_team/claude-sonnet-5-5")
	wantAt(t, doc, "targets.turno_1.llm_client", "polypus_fallback")
	wantAt(t, doc, "routes.turno.id", "router/turno")
	wantAt(t, doc, "routes.turno.type", "random")
	wantAt(t, doc, "routes.turno.targets", []any{"turno_0", "turno_1"})
	wantAt(t, doc, "routes.turno.weights", []any{int64(1), int64(0)})
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
