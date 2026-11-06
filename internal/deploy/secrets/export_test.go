package secrets

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

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
	err := validateAPIKeysFromEnv("polypus-work", []string{"CF_AI_API_KEY"})
	if err == nil || !strings.Contains(err.Error(), "polypus-work:CF_AI_API_KEY") {
		t.Fatalf("err = %v, want the short key named with its keyring service", err)
	}
}

func TestValidateAPIKeysSkipsNonKeys(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "x")
	t.Setenv("CF_ACCOUNT_ID", "x")
	if err := validateAPIKeysFromEnv("polypus-work", []string{"TS_AUTHKEY", "CF_ACCOUNT_ID"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeysAcceptsLongKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", strings.Repeat("x", minAPIKeyLen))
	if err := validateAPIKeysFromEnv("polypus-work", []string{"GEMINI_API_KEY"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCloudflareAccountID(t *testing.T) {
	t.Setenv("CF_ACCOUNT_ID", strings.Repeat("a", 32))
	if err := validateCloudflareAccountIDFromEnv("polypus-work"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CF_ACCOUNT_ID", "not-an-id")
	if err := validateCloudflareAccountIDFromEnv("polypus-work"); err == nil {
		t.Fatal("expected a malformed account ID rejected")
	}
}

func TestExportWritesGatewayEnvWithoutTailscaleKey(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "tskey-auth-example")
	t.Setenv("OPENROUTER_API_KEY", strings.Repeat("o", minAPIKeyLen))
	t.Setenv("ANTHROPIC_API_KEY", strings.Repeat("a", minAPIKeyLen))
	dir := t.TempDir()
	names := []string{"TS_AUTHKEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY"}
	if err := Export("polypus-test", names, filepath.Join(dir, ".env.deploy")); err != nil {
		t.Fatal(err)
	}
	deploy, err := NonEmptyDotenvKeys(filepath.Join(dir, ".env.deploy"))
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NonEmptyDotenvKeys(filepath.Join(dir, GatewayEnvFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if !deploy[n] {
			t.Errorf(".env.deploy is missing %s", n)
		}
	}
	if !gateway["OPENROUTER_API_KEY"] || !gateway["ANTHROPIC_API_KEY"] {
		t.Errorf("%s = %v, want both backend keys", GatewayEnvFileName, gateway)
	}
	if gateway["TS_AUTHKEY"] {
		t.Errorf("%s carries TS_AUTHKEY, which only the tailscale service reads", GatewayEnvFileName)
	}
}

func TestExportWithKeyringReadsTheGivenKeyring(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("SOPS_SECRETS_FILE", "")
	kr := operatorconfig.NewMemKeyring()
	if err := kr.Set("polypus-test", "OPENROUTER_API_KEY", strings.Repeat("k", minAPIKeyLen)); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), ".env.deploy")
	if err := ExportWithKeyring(kr, "polypus-test", []string{"OPENROUTER_API_KEY"}, out); err != nil {
		t.Fatal(err)
	}
	got, err := NonEmptyDotenvKeys(out)
	if err != nil {
		t.Fatal(err)
	}
	if !got["OPENROUTER_API_KEY"] {
		t.Fatalf("%v: want the key from the injected keyring", got)
	}
}
