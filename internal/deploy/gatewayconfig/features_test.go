package gatewayconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// homelab deploy default keyring names (mirror secrets.DefaultNames).
var homelabDefaultSecretNames = []string{"CF_AI_API_KEY", "CF_ACCOUNT_ID", "TS_AUTHKEY"}

func TestSystemOneBackendEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")

	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("systemone_backend:\n  enabled: true\n")
	ok, err := SystemOneBackendEnabled(path)
	if err != nil || !ok {
		t.Fatalf("enabled true: ok=%v err=%v", ok, err)
	}

	write("systemone_backend:\n  enabled: false\n")
	ok, err = SystemOneBackendEnabled(path)
	if err != nil || ok {
		t.Fatalf("enabled false: ok=%v err=%v", ok, err)
	}

	write("processes:\n  mlx: false\n")
	ok, err = SystemOneBackendEnabled(path)
	if err != nil || ok {
		t.Fatalf("missing block defaults off: ok=%v err=%v", ok, err)
	}
}

func TestSecretEnvNamesOpenRouterOptional(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")

	cfOnly := "backends:\n  cf_local:\n    remote: true\n    auth:\n      bearer_env: CF_AI_API_KEY\n"
	if err := os.WriteFile(path, []byte(cfOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := SecretEnvNames(path, homelabDefaultSecretNames)
	if err != nil {
		t.Fatal(err)
	}
	if contains(names, "OPENROUTER_API_KEY") {
		t.Fatalf("cf only: unexpected OPENROUTER_API_KEY in %v", names)
	}

	withOR := cfOnly + `
  openrouter:
    remote: true
    auth:
      bearer_env: OPENROUTER_API_KEY
`
	if err := os.WriteFile(path, []byte(withOR), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err = SecretEnvNames(path, homelabDefaultSecretNames)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(names, "OPENROUTER_API_KEY") {
		t.Fatalf("openrouter backend: missing OPENROUTER_API_KEY in %v", names)
	}
}

func TestSecretEnvNamesGeminiExtension(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	body := `backends:
  gemini_studio:
    remote: true
    extension: gemini
    auth:
      bearer_env: GEMINI_API_KEY
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	names, err := SecretEnvNames(path, homelabDefaultSecretNames)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(names, "GEMINI_API_KEY") {
		t.Fatalf("gemini extension: missing GEMINI_API_KEY in %v", names)
	}
}

func TestSecretEnvNamesHomelabFile(t *testing.T) {
	path := homelabFixture
	names, err := SecretEnvNames(path, homelabDefaultSecretNames)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CF_AI_API_KEY",
		"CF_ACCOUNT_ID",
		"TS_AUTHKEY",
		"OPENROUTER_API_KEY",
		"DEEPINFRA_API_KEY",
		"GEMINI_API_KEY_FREE",
		"GEMINI_API_KEY",
	} {
		if !contains(names, want) {
			t.Fatalf("homelab export-env names missing %s in %v", want, names)
		}
	}
}

func TestAllowLists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gateway.yaml")
	body := `backends:
  openrouter:
    models:
      allow:
        - meta-llama/llama-3.3-70b-instruct
  deepinfra:
    models:
      allow:
        - ibm-granite/granite-4.2-8b
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := AllowLists(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["openrouter"]) != 1 || got["deepinfra"][0] != "ibm-granite/granite-4.2-8b" {
		t.Fatalf("allow lists %v", got)
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
