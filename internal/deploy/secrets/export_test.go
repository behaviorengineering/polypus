package secrets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
)

func secretNames(t *testing.T, gatewayYAML string) []string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte(gatewayYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := gatewayconfig.SecretEnvNames(path, DefaultNames)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestSecretNamesWithoutCloudflare(t *testing.T) {
	names := secretNames(t, "backends:\n  openrouter:\n    auth:\n      bearer_env: OPENROUTER_API_KEY\n")
	if want := []string{"TS_AUTHKEY", "OPENROUTER_API_KEY"}; !slices.Equal(names, want) {
		t.Fatalf("names %v want %v", names, want)
	}
}

func TestSecretNamesWithCloudflare(t *testing.T) {
	names := secretNames(t, `backends:
  cf_local:
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/${CF_ACCOUNT_ID}/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
`)
	if want := []string{"TS_AUTHKEY", "CF_AI_API_KEY", "CF_ACCOUNT_ID"}; !slices.Equal(names, want) {
		t.Fatalf("names %v want %v", names, want)
	}
}

func TestValidateAPIKeysChecksCloudflareKey(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "short")
	err := ValidateAPIKeysFromEnv("polypus-work", []string{"CF_AI_API_KEY"})
	if err == nil || !strings.Contains(err.Error(), "polypus-work:CF_AI_API_KEY") {
		t.Fatalf("err = %v, want the short key named with its keyring service", err)
	}
}

func TestValidateAPIKeysSkipsNonKeys(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "x")
	t.Setenv("CF_ACCOUNT_ID", "x")
	if err := ValidateAPIKeysFromEnv("polypus-work", []string{"TS_AUTHKEY", "CF_ACCOUNT_ID"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeysAcceptsLongKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", strings.Repeat("x", minAPIKeyLen))
	if err := ValidateAPIKeysFromEnv("polypus-work", []string{"GEMINI_API_KEY"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCloudflareAccountID(t *testing.T) {
	t.Setenv("CF_ACCOUNT_ID", strings.Repeat("a", 32))
	if err := ValidateCloudflareAccountIDFromEnv("polypus-work"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_ACCOUNT_ID", "not-an-id")
	if err := ValidateCloudflareAccountIDFromEnv("polypus-work"); err == nil {
		t.Fatal("expected a malformed account ID rejected")
	}
}
