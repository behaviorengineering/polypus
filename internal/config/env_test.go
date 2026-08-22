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

func TestInferenceCloudCaseAllowed(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	if !InferenceCloudCaseAllowed() {
		t.Fatal("expected allowed")
	}
	t.Setenv("INFERENCE_CLOUD_CASE", "0")
	if InferenceCloudCaseAllowed() {
		t.Fatal("expected disabled")
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

func TestLoadRouterConfigRemoteFields(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")
	t.Setenv("CF_ACCOUNT_ID", "acct")

	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
default_chat_backend: cf_local
default_tts_backend: mlx_local
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

func TestStripRemoteBackendsWhenDisabled(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "0")
	dir := t.TempDir()
	path := dir + "/config.yaml"
	content := `
default_tts_backend: cf_local
default_chat_backend: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/x/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, tts]
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
	if _, ok := cfg.Backends["cf_local"]; ok {
		t.Fatal("cf_local should be stripped without cloud opt-in")
	}
	if cfg.DefaultTTSBackend != "mlx_local" {
		t.Fatalf("default tts: %q", cfg.DefaultTTSBackend)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
