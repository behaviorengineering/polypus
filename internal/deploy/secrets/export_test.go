package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"

	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
	err := validateAPIKeys("polypus-work", []string{"CF_AI_API_KEY"}, map[string]string{"CF_AI_API_KEY": "short"})
	if err == nil || !strings.Contains(err.Error(), "polypus-work:CF_AI_API_KEY") {
		t.Fatalf("err = %v, want the short key named with its keyring service", err)
	}
}

func TestValidateAPIKeysSkipsNonKeys(t *testing.T) {
	if err := validateAPIKeys("polypus-work", []string{"TS_AUTHKEY", "CF_ACCOUNT_ID"}, map[string]string{"TS_AUTHKEY": "x", "CF_ACCOUNT_ID": "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAPIKeysAcceptsLongKey(t *testing.T) {
	if err := validateAPIKeys("polypus-work", []string{"GEMINI_API_KEY"}, map[string]string{"GEMINI_API_KEY": strings.Repeat("x", minAPIKeyLen)}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCloudflareAccountID(t *testing.T) {
	if err := validateCloudflareAccountID("polypus-work", strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if err := validateCloudflareAccountID("polypus-work", "not-an-id"); err == nil {
		t.Fatal("expected a malformed account ID rejected")
	}
}

// shellOnly reads the shell only, as CI does with --secrets-from env.
func shellOnly() *Resolver { return &Resolver{Prefer: SourceEnv} }

func TestExportWritesGatewayEnvWithoutTailscaleKey(t *testing.T) {
	t.Setenv("TS_AUTHKEY", "tskey-auth-example")
	t.Setenv("OPENROUTER_API_KEY", strings.Repeat("o", minAPIKeyLen))
	t.Setenv("ANTHROPIC_API_KEY", strings.Repeat("a", minAPIKeyLen))
	dir := t.TempDir()
	names := []string{"TS_AUTHKEY", "OPENROUTER_API_KEY", "ANTHROPIC_API_KEY"}
	if err := Export(shellOnly(), "polypus-test", names, filepath.Join(dir, ".env.deploy")); err != nil {
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

// TestExportSetsNothingInTheProcessEnvironment checks keyring values go straight
// to the dotenv files, so docker children inherit no secrets.
func TestExportSetsNothingInTheProcessEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	kr := operatorconfig.NewMemKeyring()
	value := strings.Repeat("k", minAPIKeyLen) + " #quoted"
	if err := kr.Set("polypus-test", "OPENROUTER_API_KEY", value); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), ".env.deploy")
	if err := Export(&Resolver{Keyring: kr}, "polypus-test", []string{"OPENROUTER_API_KEY"}, out); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("OPENROUTER_API_KEY"); got != "" {
		t.Fatalf("OPENROUTER_API_KEY = %q in the process environment, want unset", got)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "OPENROUTER_API_KEY=" + strconv.Quote(value) + "\n"; string(data) != want {
		t.Fatalf(".env.deploy = %q, want %q", data, want)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestExportMissingSecretSaysWhereItLooked(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	err := Export(&Resolver{Keyring: operatorconfig.NewMemKeyring()}, "polypus-test", []string{"OPENROUTER_API_KEY"}, filepath.Join(t.TempDir(), ".env.deploy"))
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition || !strings.Contains(err.Error(), "not in this shell, keyring service polypus-test or its secrets file") {
		t.Fatalf("err = %v, want the missing secret named with where it was looked for", err)
	}
}

func TestExportConflictWritesNothing(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", strings.Repeat("s", minAPIKeyLen))
	kr := operatorconfig.NewMemKeyring()
	if err := kr.Set("polypus-test", "OPENROUTER_API_KEY", strings.Repeat("k", minAPIKeyLen)); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), ".env.deploy")
	err := Export(&Resolver{Keyring: kr}, "polypus-test", []string{"OPENROUTER_API_KEY"}, out)
	var c *Conflict
	if !errors.As(err, &c) {
		t.Fatalf("err = %v, want a *Conflict", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf(".env.deploy should not exist after a conflict (stat err %v)", statErr)
	}
}

// TestExportInvalidKeyWritesNothing checks a key that fails its shape check leaves
// neither dotenv file behind, for the account ID and for a short API key.
func TestExportInvalidKeyWritesNothing(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"short API key":     {"TS_AUTHKEY": "ts", "OPENROUTER_API_KEY": "short", "CF_ACCOUNT_ID": strings.Repeat("a", 32)},
		"malformed account": {"TS_AUTHKEY": "ts", "OPENROUTER_API_KEY": strings.Repeat("k", minAPIKeyLen), "CF_ACCOUNT_ID": "not-an-id"},
	} {
		t.Run(name, func(t *testing.T) {
			for k, v := range env {
				t.Setenv(k, v)
			}
			out := filepath.Join(t.TempDir(), ".env.deploy")
			err := Export(&Resolver{Keyring: operatorconfig.NewMemKeyring()}, "polypus-work", []string{"TS_AUTHKEY", "OPENROUTER_API_KEY", "CF_ACCOUNT_ID"}, out)
			if err == nil {
				t.Fatal("expected the bad value refused")
			}
			for _, p := range []string{out, filepath.Join(filepath.Dir(out), GatewayEnvFileName)} {
				if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
					t.Errorf("%s should not exist after a failed check (stat err %v)", filepath.Base(p), statErr)
				}
			}
		})
	}
}
