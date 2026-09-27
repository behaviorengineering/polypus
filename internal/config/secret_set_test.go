package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

func TestRequireDeclaredSecretMatchesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
secrets:
  - CF_AI_API_KEY
  - env: CF_ACCOUNT_ID
    required: false
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	if err := RequireDeclaredSecret("CF_AI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if err := RequireDeclaredSecret("CF_ACCOUNT_ID"); err != nil {
		t.Fatal(err)
	}
	err := RequireDeclaredSecret("OTHER_KEY")
	if err == nil {
		t.Fatal("expected error for undeclared name")
	}
	if !strings.Contains(err.Error(), "OTHER_KEY") {
		t.Fatalf("error %v", err)
	}
}

func TestRequireDeclaredSecretNoSecretsBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	err := RequireDeclaredSecret("CF_AI_API_KEY")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("error %v", err)
	}
}

func TestSetPolypusSecretWritesInjectedKeyring(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
secrets:
  - CF_AI_API_KEY
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	mem := operatorconfig.NewMemKeyring()
	if err := SetPolypusSecret("CF_AI_API_KEY", "test-token", mem); err != nil {
		t.Fatal(err)
	}
	got, err := mem.Get("polypus", "CF_AI_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "test-token" {
		t.Fatalf("got %q", got)
	}
}

func TestSetPolypusSecretDoesNotWriteUndeclared(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("secrets:\n  - CF_AI_API_KEY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	mem := operatorconfig.NewMemKeyring()
	err := SetPolypusSecret("OTHER_KEY", "nope", mem)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, getErr := mem.Get("polypus", "OTHER_KEY"); getErr == nil {
		t.Fatal("undeclared name was written")
	}
}
