package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
)

func TestValidateOptionalAPIKeysSkipsCloudflare(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	err := ValidateOptionalAPIKeysFromEnv([]string{"CF_AI_API_KEY", "OPENROUTER_API_KEY"})
	if err == nil {
		t.Fatal("expected short OPENROUTER_API_KEY error")
	}
}

func TestSecretEnvNamesIntegrationNoOpenRouter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	body := `backends:
  cf_local:
    remote: true
    auth:
      bearer_env: CF_AI_API_KEY
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := gatewayconfig.SecretEnvNames(path, DefaultNames)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n == "OPENROUTER_API_KEY" {
			t.Fatalf("should not require OPENROUTER_API_KEY: %v", names)
		}
	}
}

func TestSecretEnvNamesIntegrationWithOpenRouter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	body := `backends:
  openrouter:
    remote: true
    auth:
      bearer_env: OPENROUTER_API_KEY
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := gatewayconfig.SecretEnvNames(path, DefaultNames)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range names {
		if n == "OPENROUTER_API_KEY" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected OPENROUTER_API_KEY in %v", names)
	}
}

func TestValidateOptionalAPIKeysAcceptsLongKey(t *testing.T) {
	long := strings.Repeat("x", minAPIKeyLen)
	t.Setenv("GEMINI_API_KEY", long)
	if err := ValidateOptionalAPIKeysFromEnv([]string{"GEMINI_API_KEY"}); err != nil {
		t.Fatal(err)
	}
}
