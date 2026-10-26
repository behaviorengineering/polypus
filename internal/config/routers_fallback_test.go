package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fallbackBackendsYAML = `
chat_backend:
  enabled: true
  default: claude_team
backends:
  claude_team:
    remote: true
    extension: anthropic
    auth:
      bearer_env: FALLBACK_TEST_KEY
    capabilities: [chat]
  openrouter:
    base_url: http://127.0.0.1:1324/v1
    capabilities: [chat]
`

func loadFallbackConfig(t *testing.T, routers string) (RouterConfig, error) {
	t.Helper()
	t.Setenv("FALLBACK_TEST_KEY", "secret-key-at-least-twenty-ch")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(fallbackBackendsYAML+routers), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	return LoadRouterConfig(ServeOptions{})
}

func TestLoadRouterFallbackRoute(t *testing.T) {
	cfg, err := loadFallbackConfig(t, `
routers:
  turno:
    capability: chat
    route:
      type: fallback
      targets:
        - claude_team/claude-sonnet-5-5
        - openrouter/anthropic/claude-sonnet-5.5
`)
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Routers["turno"]
	if !r.IsComposed() || r.Route.Type != RouteFallback {
		t.Fatalf("route: %+v", r)
	}
	want := []string{"claude_team/claude-sonnet-5-5", "openrouter/anthropic/claude-sonnet-5.5"}
	if strings.Join(r.Route.FallbackTargets, ",") != strings.Join(want, ",") {
		t.Fatalf("order: %v", r.Route.FallbackTargets)
	}
	if !cfg.HasComposedRouters() || cfg.Switchyard.BaseURL == "" {
		t.Fatal("a fallback route needs Switchyard")
	}
}

func TestLoadRouterFallbackRejects(t *testing.T) {
	cases := map[string]string{
		"one target": `
      targets:
        - claude_team/claude-sonnet-5-5
`,
		"duplicate target": `
      targets:
        - claude_team/claude-sonnet-5-5
        - claude_team/claude-sonnet-5-5
`,
		"mapping targets": `
      targets:
        first: claude_team/claude-sonnet-5-5
        second: openrouter/anthropic/claude-sonnet-5.5
`,
		"stage field": `
      picker: efficient_first
      targets:
        - claude_team/claude-sonnet-5-5
        - openrouter/anthropic/claude-sonnet-5.5
`,
		"unknown backend": `
      targets:
        - claude_team/claude-sonnet-5-5
        - nowhere/model
`,
	}
	for name, route := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadFallbackConfig(t, `
routers:
  turno:
    capability: chat
    route:
      type: fallback`+route)
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadRouterClassifierRejectsListTargets(t *testing.T) {
	_, err := loadFallbackConfig(t, `
routers:
  pick:
    capability: chat
    route:
      type: llm_classifier
      mode: custom
      classifier: claude_team/claude-haiku-5-5
      targets:
        - claude_team/claude-sonnet-5-5
        - openrouter/anthropic/claude-sonnet-5.5
`)
	if err == nil || !strings.Contains(err.Error(), "must be a mapping") {
		t.Fatalf("err: %v", err)
	}
}
