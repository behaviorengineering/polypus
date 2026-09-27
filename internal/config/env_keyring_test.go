package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

func TestLoadRouterConfigFillsCFTokenFromKeyring(t *testing.T) {
	mem := operatorconfig.NewMemKeyring()
	if err := mem.Set("polypus", "CF_AI_API_KEY", "test-token"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_AI_API_KEY", "")

	dir := t.TempDir()
	content := `
secrets:
  - CF_AI_API_KEY
chat_backend:
  enabled: true
  default: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai/run
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("CF_ACCOUNT_ID", "acct-1")

	_, err := LoadRouterConfig(ServeOptions{Keyring: mem})
	if err != nil {
		t.Fatal(err)
	}
	auth := BackendAuth{BearerEnv: "CF_AI_API_KEY"}
	tok, err := auth.ResolveBearerToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok != "test-token" {
		t.Fatalf("token %q", tok)
	}
}

func TestLoadRouterConfigSecretMappingForm(t *testing.T) {
	mem := operatorconfig.NewMemKeyring()
	if err := mem.Set("polypus", "API_KEY", "mapped"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_KEY", "")

	dir := t.TempDir()
	content := `
secrets:
  - env: API_KEY
    required: true
backends:
  remote_one:
    remote: true
    base_url: http://127.0.0.1:9/v1
    auth:
      bearer_env: API_KEY
    capabilities: [chat]
chat_backend:
  enabled: true
  default: remote_one
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	_, err := LoadRouterConfig(ServeOptions{Keyring: mem})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("API_KEY") != "mapped" {
		t.Fatalf("API_KEY %q", os.Getenv("API_KEY"))
	}
}

func TestLoadRouterConfigSkipsKeyringWithoutSecretsBlock(t *testing.T) {
	boom := &boomKeyring{}
	dir := t.TempDir()
	content := `
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	_, err := LoadRouterConfig(ServeOptions{Keyring: boom})
	if err != nil {
		t.Fatal(err)
	}
	if boom.called {
		t.Fatal("expected no keyring access without secrets block")
	}
}

type boomKeyring struct {
	called bool
}

func (b *boomKeyring) Get(service, account string) (string, error) {
	b.called = true
	return "", fmt.Errorf("unexpected keyring get")
}

func (b *boomKeyring) Set(service, account, secret string) error { return nil }
func (b *boomKeyring) Delete(service, account string) error      { return nil }
