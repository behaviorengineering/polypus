package gatewayconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChatSmokeTargetsHomelabShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	body := `backends:
  cf_local:
    extension: cloudflare
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat]
    models:
      allow:
        - "@cf/ibm-granite/granite-4.0-h-micro"
  openrouter:
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [chat]
    models:
      allow:
        - mistralai/mistral-nemo
        - google/gemini-2.5-flash
        - hexgrad/kokoro-82m
  gemini_free:
    extension: gemini
    auth:
      bearer_env: GEMINI_API_KEY_FREE
    capabilities: [chat]
    models:
      allow:
        - gemma-4-26b-a4b-it
  gemini_paid:
    extension: gemini
    auth:
      bearer_env: GEMINI_API_KEY_PAID
    capabilities: [chat]
    models:
      allow:
        - gemini-2.5-flash
  deepinfra:
    auth:
      bearer_env: DEEPINFRA_API_KEY
    capabilities: [chat]
    models:
      allow:
        - deepseek-ai/DeepSeek-V4-Flash-0731
        - google/gemma-4-26B-A4B-it
        - google/gemma-4-31B-it
        - google/gemma-3-27b-it
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	targets, err := ChatSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 7 {
		t.Fatalf("expected 7 targets, got %d: %+v", len(targets), targets)
	}
	models := map[string]ChatSmokeTarget{}
	for _, tg := range targets {
		models[tg.Model] = tg
	}
	cf := models["cf_local/@cf/ibm-granite/granite-4.0-h-micro"]
	if !contains(cf.SecretEnv, "CF_ACCOUNT_ID") {
		t.Fatalf("cf_local secrets: %v", cf.SecretEnv)
	}
	if models["gemini_free/gemma-4-26b-a4b-it"].BackendID != "" {
		t.Fatal("gemini_free must be skipped in deploy smoke")
	}
	gp := models["gemini_paid/gemini-2.5-flash"]
	if gp.ReasoningEffort != "minimal" {
		t.Fatalf("gemini reasoning: %q", gp.ReasoningEffort)
	}
	if models["openrouter/mistralai/mistral-nemo"].BackendID == "" {
		t.Fatal("missing openrouter mistral target")
	}
	if models["openrouter/google/gemini-2.5-flash"].BackendID == "" {
		t.Fatal("missing openrouter gemini target")
	}
	if models["deepinfra/deepseek-ai/DeepSeek-V4-Flash-0731"].BackendID == "" {
		t.Fatal("missing deepinfra deepseek target")
	}
	g26 := models["deepinfra/google/gemma-4-26B-A4B-it"]
	if g26.BackendID == "" || g26.ReasoningEffort != "minimal" {
		t.Fatalf("deepinfra gemma 26b: %+v", g26)
	}
	g31 := models["deepinfra/google/gemma-4-31B-it"]
	if g31.BackendID == "" || g31.ReasoningEffort != "minimal" {
		t.Fatalf("deepinfra gemma 31b: %+v", g31)
	}
	if models["deepinfra/google/gemma-3-27b-it"].BackendID != "" {
		t.Fatal("gemma-3 must not be a deploy smoke target")
	}
}

func TestChatSmokeTargetsHomelabFile(t *testing.T) {
	path := homelabFixture
	targets, err := ChatSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"cf_local/@cf/ibm-granite/granite-4.0-h-micro": false,
		"openrouter/mistralai/mistral-nemo":            false,
		"openrouter/google/gemini-2.5-flash":           false,
		"deepinfra/deepseek-ai/DeepSeek-V4-Flash-0731": false,
		"deepinfra/google/gemma-4-26B-A4B-it":          false,
		"deepinfra/google/gemma-4-31B-it":              false,
		"gemini_paid/gemini-2.5-flash":                 false,
	}
	for _, tg := range targets {
		if _, ok := want[tg.Model]; ok {
			want[tg.Model] = true
		}
	}
	for model, seen := range want {
		if !seen {
			t.Fatalf("missing homelab smoke target %s in %+v", model, targets)
		}
	}
}

func TestSecretsReady(t *testing.T) {
	tg := ChatSmokeTarget{SecretEnv: []string{"A", "B"}}
	present := map[string]bool{"A": true}
	if SecretsReady(tg, present) {
		t.Fatal("expected not ready")
	}
	present["B"] = true
	if !SecretsReady(tg, present) {
		t.Fatal("expected ready")
	}
}

func TestDeepInfraChatTargetsSkipsUltraTurboAndGemma3(t *testing.T) {
	got := deepInfraChatTargets("deepinfra", []string{
		"google/gemma-3-27b-it",
		"google/gemma-4-31B-it-turbo",
		"google/gemma-4-31B-it-Ultra",
		"google/gemma-4-31B-it",
		"google/gemma-4-26B-A4B-it",
		"deepseek-ai/DeepSeek-V4-Flash-0731",
	})
	if len(got) != 3 {
		t.Fatalf("len=%d %+v", len(got), got)
	}
	if got[0].Model != "deepinfra/deepseek-ai/DeepSeek-V4-Flash-0731" {
		t.Fatalf("deepseek: %q", got[0].Model)
	}
	if got[1].Model != "deepinfra/google/gemma-4-26B-A4B-it" || got[1].ReasoningEffort != "minimal" {
		t.Fatalf("26b: %+v", got[1])
	}
	if got[2].Model != "deepinfra/google/gemma-4-31B-it" || got[2].ReasoningEffort != "minimal" {
		t.Fatalf("31b: %+v", got[2])
	}
}
