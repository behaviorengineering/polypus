package config

import (
	"os"
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("CF_ACCOUNT_ID", "acct-test")
	got := ExpandEnv("https://example.com/accounts/${CF_ACCOUNT_ID}/ai/v1")
	want := "https://example.com/accounts/acct-test/ai/v1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExpandEnvTrimsSubstitution(t *testing.T) {
	t.Setenv("CF_ACCOUNT_ID", "acct-test\n")
	got := ExpandEnv("https://example.com/accounts/${CF_ACCOUNT_ID}/ai/v1")
	want := "https://example.com/accounts/acct-test/ai/v1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBackendAuthResolveBearerToken(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret-token")
	token, err := BackendAuth{BearerEnv: "CF_AI_API_KEY"}.ResolveBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	if token != "secret-token" {
		t.Fatalf("got %q", token)
	}
}

func TestLoadRouterGeminiExtensionOmitsBaseURL(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "secret-key-at-least-twenty-ch")

	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
chat_backend:
  enabled: true
  default: gemini_studio
backends:
  gemini_studio:
    remote: true
    extension: gemini
    auth:
      bearer_env: GEMINI_API_KEY
    capabilities: [chat]
    models:
      allow:
        - gemma-4-26b-a4b-it
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backends["gemini_studio"]
	if !b.Remote || !b.IsGeminiExtension() {
		t.Fatalf("backend: %+v", b)
	}
	if b.BaseURL != "" {
		t.Fatalf("gemini base_url should stay empty for Bifrost default, got %q", b.BaseURL)
	}
}

func TestLoadRouterRemoteRequiresBaseURL(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret-key-at-least-twenty-ch")

	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
chat_backend:
  enabled: true
  default: openrouter
backends:
  openrouter:
    remote: true
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [chat]
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	if _, err := LoadRouterConfig(ServeOptions{}); err == nil {
		t.Fatal("expected base_url required for non-gemini remote")
	}
}

func TestLoadRouterConfigRemoteFields(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret")
	t.Setenv("CF_ACCOUNT_ID", "acct")

	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
chat_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat]
    models:
      allow:
        - "@cf/zai-org/glm-4.7-flash"
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	cfg, err := LoadRouterConfig(ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backends["cf_local"]
	if !b.Remote || !b.IsCloudflareExtension() {
		t.Fatalf("backend: %+v", b)
	}
	if b.BaseURL != "https://api.cloudflare.com/client/v4/accounts/acct/ai/v1" {
		t.Fatalf("base_url: %q", b.BaseURL)
	}
}

func TestRemoteBackendLoadsWithCredentialsOnly(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret")
	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/x/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [tts, stt, voices]
`
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Backends["cf_local"]; !ok {
		t.Fatal("cf_local should load when credentials are set")
	}
	if !cfg.TTS.Enabled || cfg.TTS.Default != "cf_local" {
		t.Fatalf("tts: %+v", cfg.TTS)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
