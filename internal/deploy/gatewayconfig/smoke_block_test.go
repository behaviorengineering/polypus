package gatewayconfig

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeGateway writes body as a gateway YAML in a temp dir and returns its path.
func writeGateway(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// targetModels lists every chat and SystemOne target model for path.
func targetModels(t *testing.T, path string) (chat, systemOne []string) {
	t.Helper()
	ct, err := ChatSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := SystemOneSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range ct {
		chat = append(chat, tg.Model)
	}
	for _, tg := range st {
		systemOne = append(systemOne, tg.Model)
	}
	return chat, systemOne
}

func TestSmokeTargetsRespectSkip(t *testing.T) {
	path := writeGateway(t, `backends:
  anything_local:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat, systemone]
    models:
      allow: [qwen2.5-7b]
      systemone_allow: [decider]
    smoke:
      skip: true
  or_skipped:
    base_url: https://openrouter.ai/api/v1
    capabilities: [chat, systemone]
    models:
      allow: [mistralai/mistral-nemo]
      systemone_allow: [perplexity/pplx-decider-v1.2]
    smoke:
      skip: true
  kept:
    base_url: http://127.0.0.1:1235/v1
    capabilities: [chat]
    models:
      allow: [llama]
`)
	chat, systemOne := targetModels(t, path)
	if !slices.Equal(chat, []string{"kept/llama"}) {
		t.Fatalf("chat targets %v: only the backend without smoke.skip gets one", chat)
	}
	if len(systemOne) != 0 {
		t.Fatalf("systemone targets %v: skipped backends get none", systemOne)
	}
}

func TestSmokeTargetsUseExplicitModels(t *testing.T) {
	path := writeGateway(t, `backends:
  openrouter:
    base_url: https://openrouter.ai/api/v1
    capabilities: [chat, systemone]
    models:
      allow: [mistralai/mistral-nemo, google/gemini-2.5-flash, openai/gpt-oss-20b]
      systemone_allow: [perplexity/pplx-decider-v1.2]
    smoke:
      chat: [openai/gpt-oss-20b]
      systemone: inception/mercury-decide
  studio:
    extension: gemini
    capabilities: [chat]
    models:
      allow: [gemini-2.5-flash]
    smoke:
      chat: [gemini-3-flash-preview]
`)
	ct, err := ChatSmokeTargets(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []ChatSmokeTarget{
		{BackendID: "openrouter", Model: "openrouter/openai/gpt-oss-20b"},
		{BackendID: "studio", Model: "studio/gemini-3-flash-preview", ReasoningEffort: minimalReasoning, SecretEnv: []string{"GEMINI_API_KEY"}},
	}
	if len(ct) != len(want) {
		t.Fatalf("chat targets %+v want %+v", ct, want)
	}
	for i := range want {
		if ct[i].BackendID != want[i].BackendID || ct[i].Model != want[i].Model ||
			ct[i].ReasoningEffort != want[i].ReasoningEffort || !slices.Equal(ct[i].SecretEnv, want[i].SecretEnv) {
			t.Fatalf("chat target %d: %+v want %+v", i, ct[i], want[i])
		}
	}
	_, systemOne := targetModels(t, path)
	if !slices.Equal(systemOne, []string{"openrouter/inception/mercury-decide"}) {
		t.Fatalf("systemone targets %v: smoke.systemone overrides the decider pick", systemOne)
	}
}

// providerBlocks is a gateway YAML with OpenRouter, DeepInfra and Cloudflare
// backends; %s are the three backend ids.
const providerBlocks = `backends:
  %s:
    base_url: https://openrouter.ai/api/v1
    auth: {bearer_env: OPENROUTER_API_KEY}
    capabilities: [chat, systemone]
    models:
      allow: [meta-llama/llama-3.3-70b-instruct, mistralai/mistral-nemo, google/gemini-2.5-flash]
      systemone_allow: [perplexity/pplx-decider-flash-v1.2, perplexity/pplx-decider-v1.2]
  %s:
    base_url: https://api.deepinfra.com/v1/openai
    auth: {bearer_env: DEEPINFRA_API_KEY}
    capabilities: [chat]
    models:
      allow: [google/gemma-3-27b-it, deepseek-ai/DeepSeek-V4-Flash-0731, google/gemma-4-26B-A4B-it]
  %s:
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai/v1
    auth: {bearer_env: CF_AI_API_KEY}
    capabilities: [chat, systemone]
    models:
      allow: ["@cf/baai/bge-m3", "@cf/ibm-granite/granite-4.0-h-micro"]
`

func TestSmokeTargetsIgnoreBackendNames(t *testing.T) {
	strip := func(models []string) []string {
		out := make([]string, 0, len(models))
		for _, m := range models {
			_, slug, _ := strings.Cut(m, "/")
			out = append(out, slug)
		}
		slices.Sort(out)
		return out
	}
	named := func(or, di, cf string) (chat, s1 []string) {
		body := providerBlocks
		for _, id := range []string{or, di, cf} {
			body = strings.Replace(body, "%s", id, 1)
		}
		return targetModels(t, writeGateway(t, body))
	}
	baseChat, baseS1 := named("openrouter", "deepinfra", "cf_local")
	renamedChat, renamedS1 := named("or_prod", "di_prod", "workers_ai")
	if !slices.Equal(strip(baseChat), strip(renamedChat)) {
		t.Fatalf("chat targets differ by backend name: %v vs %v", baseChat, renamedChat)
	}
	if !slices.Equal(strip(baseS1), strip(renamedS1)) {
		t.Fatalf("systemone targets differ by backend name: %v vs %v", baseS1, renamedS1)
	}
	wantChat := []string{
		"@cf/ibm-granite/granite-4.0-h-micro",
		"deepseek-ai/DeepSeek-V4-Flash-0731",
		"google/gemini-2.5-flash",
		"google/gemma-4-26B-A4B-it",
		"mistralai/mistral-nemo",
	}
	if got := strip(renamedChat); !slices.Equal(got, wantChat) {
		t.Fatalf("renamed chat slugs %v want %v", got, wantChat)
	}
	wantS1 := []string{"perplexity/pplx-decider-v1.2", "typesafe/jev"}
	if got := strip(renamedS1); !slices.Equal(got, wantS1) {
		t.Fatalf("renamed systemone slugs %v want %v", got, wantS1)
	}
}
