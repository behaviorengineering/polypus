package smoke

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunWithProviders(t *testing.T) {
	dir := t.TempDir()
	gw := filepath.Join(dir, "gw.yaml")
	if err := os.WriteFile(gw, []byte(`backends:
  cf_local:
    extension: cloudflare
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat]
    models:
      allow: ["@cf/ibm-granite/granite-4.0-h-micro"]
  openrouter:
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [chat]
    models:
      allow: ["google/gemma-4-26b-a4b-it:free"]
  deepinfra:
    auth:
      bearer_env: DEEPINFRA_API_KEY
    capabilities: [chat]
    models:
      allow:
        - deepseek-ai/DeepSeek-V4-Flash-0731
        - google/gemma-4-26B-A4B-it
`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, ".env.deploy")
	if err := os.WriteFile(env, []byte("CF_AI_API_KEY=x\nCF_ACCOUNT_ID=1\nOPENROUTER_API_KEY=or\nDEEPINFRA_API_KEY=di-key-xxxxxxxxxxxxxxxx\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		body, _ := readBody(r)
		var raw map[string]any
		_ = json.Unmarshal([]byte(body), &raw)
		if m, ok := raw["model"].(string); ok {
			models = append(models, m)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, Options{
		BaseURL:       srv.URL,
		WithProviders: true,
		GatewayConfig: gw,
		EnvDeploy:     env,
	}); err != nil {
		t.Fatal(err)
	}
	if len(models) < 3 {
		t.Fatalf("expected cf_local plus two deepinfra probes, got %v", models)
	}
	want := map[string]bool{
		"cf_local/@cf/ibm-granite/granite-4.0-h-micro": false,
		"deepinfra/deepseek-ai/DeepSeek-V4-Flash-0731": false,
		"deepinfra/google/gemma-4-26B-A4B-it":          false,
	}
	for _, m := range models {
		if _, ok := want[m]; ok {
			want[m] = true
		}
	}
	for model, seen := range want {
		if !seen {
			t.Fatalf("missing probe %s in %v", model, models)
		}
	}
}

func TestRunWithDeepInfraGemmaReasoning(t *testing.T) {
	dir := t.TempDir()
	gw := filepath.Join(dir, "gw.yaml")
	if err := os.WriteFile(gw, []byte(`backends:
  deepinfra:
    auth:
      bearer_env: DEEPINFRA_API_KEY
    capabilities: [chat]
    models:
      allow:
        - google/gemma-4-31B-it
        - google/gemma-3-27b-it
`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, ".env.deploy")
	if err := os.WriteFile(env, []byte("DEEPINFRA_API_KEY=di-key-xxxxxxxxxxxxxxxx\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var models []string
	var efforts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readBody(r)
		var raw map[string]any
		_ = json.Unmarshal([]byte(body), &raw)
		if m, ok := raw["model"].(string); ok {
			models = append(models, m)
		}
		effort := ""
		if rec, ok := raw["reasoning"].(map[string]any); ok {
			if v, ok := rec["effort"].(string); ok {
				effort = v
			}
		}
		efforts = append(efforts, effort)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, Options{
		BaseURL:       srv.URL,
		WithProviders: true,
		GatewayConfig: gw,
		EnvDeploy:     env,
	}); err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != "deepinfra/google/gemma-4-31B-it" {
		t.Fatalf("models=%v", models)
	}
	if len(efforts) != 1 || efforts[0] != "minimal" {
		t.Fatalf("efforts=%v", efforts)
	}
}

func TestRunWithProvidersSkipsMissingSecrets(t *testing.T) {
	dir := t.TempDir()
	gw := filepath.Join(dir, "gw.yaml")
	if err := os.WriteFile(gw, []byte(`backends:
  cf_local:
    extension: cloudflare
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat]
    models:
      allow: ["@cf/ibm-granite/granite-4.0-h-micro"]
  openrouter:
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [chat]
    models:
      allow: ["google/gemma-4-26b-a4b-it:free"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, ".env.deploy")
	if err := os.WriteFile(env, []byte("CF_AI_API_KEY=x\nCF_ACCOUNT_ID=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := readBody(r)
		var raw map[string]any
		_ = json.Unmarshal([]byte(body), &raw)
		if m, ok := raw["model"].(string); ok {
			models = append(models, m)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, Options{
		BaseURL:       srv.URL,
		WithProviders: true,
		GatewayConfig: gw,
		EnvDeploy:     env,
	}); err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0] != "cf_local/@cf/ibm-granite/granite-4.0-h-micro" {
		t.Fatalf("expected only cf_local probe, got %v", models)
	}
}
