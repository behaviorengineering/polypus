package gatewayconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemOneSmokeTargetsOpenRouterDecider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	body := `backends:
  cf_local:
    extension: cloudflare
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, systemone]
    models:
      allow:
        - typesafe/jev
  openrouter:
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [chat, systemone]
    models:
      allow:
        - mistralai/mistral-nemo
      systemone_allow:
        - perplexity/pplx-decider-flash-v1.2
        - perplexity/pplx-decider-v1.2
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	targets, err := SystemOneSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d: %+v", len(targets), targets)
	}
	models := map[string]ChatSmokeTarget{}
	for _, tg := range targets {
		models[tg.Model] = tg
	}
	cf := models["cf_local/typesafe/jev"]
	if cf.BackendID != "cf_local" || !contains(cf.SecretEnv, "CF_ACCOUNT_ID") {
		t.Fatalf("cf_local: %+v", cf)
	}
	or := models["openrouter/perplexity/pplx-decider-v1.2"]
	if or.BackendID != "openrouter" {
		t.Fatalf("openrouter: %+v", or)
	}
	if models["openrouter/perplexity/pplx-decider-flash-v1.2"].BackendID != "" {
		t.Fatal("flash decider must not be a second deploy smoke target")
	}
}

func TestSystemOneSmokeTargetsHomelabFile(t *testing.T) {
	path := homelabFixture
	targets, err := SystemOneSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"cf_local/typesafe/jev":                   false,
		"openrouter/perplexity/pplx-decider-v1.2": false,
	}
	for _, tg := range targets {
		if _, ok := want[tg.Model]; ok {
			want[tg.Model] = true
		}
	}
	for model, seen := range want {
		if !seen {
			t.Fatalf("missing homelab systemone smoke target %s in %+v", model, targets)
		}
	}
}

func TestPickOpenRouterDeciderPrefersV12(t *testing.T) {
	got := pickOpenRouterDecider([]string{
		"perplexity/pplx-decider-flash-v1.2",
		"perplexity/pplx-decider-v1.2",
	})
	if got != openRouterDeciderV {
		t.Fatalf("got %q", got)
	}
	got = pickOpenRouterDecider([]string{"perplexity/pplx-decider-flash-v1.2"})
	if got != "perplexity/pplx-decider-flash-v1.2" {
		t.Fatalf("flash fallback: %q", got)
	}
	if pickOpenRouterDecider([]string{"mistralai/mistral-nemo"}) != "" {
		t.Fatal("chat slug must not become a decider target")
	}
}
