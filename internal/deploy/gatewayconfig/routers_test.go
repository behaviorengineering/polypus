package gatewayconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRouterSmokeTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	body := `routers:
  turno:
    capability: chat
    route:
      type: passthrough
      target: openrouter/anthropic/claude-sonnet-5.5
  investigator:
    capability: chat
    route:
      type: stage_router
      capable: cf_local/big
      efficient: cf_local/small
  backup:
    capability: chat
    route:
      type: fallback
      targets:
        - claude_team/claude-sonnet-5-5
        - openrouter/anthropic/claude-sonnet-5.5
  smart:
    capability: chat
    route:
      type: llm_classifier
      targets:
        premium: cf_local/big
        fast: cf_local/small
  voice:
    capability: tts
    route:
      type: passthrough
      target: openrouter/hexgrad/kokoro-82m
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := RouterSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []RouterTarget{
		{Model: "router/backup", Composed: true, Leaves: []string{"claude_team/claude-sonnet-5-5", "openrouter/anthropic/claude-sonnet-5.5"}},
		{Model: "router/investigator", Composed: true, Leaves: []string{"cf_local/big", "cf_local/small"}},
		{Model: "router/smart", Composed: true, Leaves: []string{"cf_local/small", "cf_local/big"}},
		{Model: "router/turno", Composed: false, Leaves: []string{"openrouter/anthropic/claude-sonnet-5.5"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets\n%+v\nwant\n%+v", got, want)
	}
}

func TestRouterSmokeTargetsHomelab(t *testing.T) {
	got, err := RouterSmokeTargets(homelabFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0].Model != "router/investigator" {
		t.Fatalf("homelab routers %+v, want router/investigator", got)
	}
}

func TestRouterSmokeTargetsUnknownType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte("routers:\n  x:\n    capability: chat\n    route:\n      type: magic\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RouterSmokeTargets(path); err == nil {
		t.Fatal("expected an unknown route type rejected")
	}
}
