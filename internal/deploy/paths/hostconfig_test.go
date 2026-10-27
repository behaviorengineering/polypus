package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveGatewayConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "gateway.yaml")
	if err := os.WriteFile(cfg, []byte("processes:\n  mlx: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveGatewayConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != cfg {
		t.Fatalf("got %q want %q", got, cfg)
	}
}

func TestResolveGatewayConfigRequired(t *testing.T) {
	if _, err := ResolveGatewayConfig(" "); err == nil {
		t.Fatal("expected error for an empty gateway config path")
	}
}

func TestResolveGatewayConfigMissing(t *testing.T) {
	if _, err := ResolveGatewayConfig(filepath.Join(t.TempDir(), "gateway.yaml")); err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestResolveGatewayConfigDirectory(t *testing.T) {
	if _, err := ResolveGatewayConfig(t.TempDir()); err == nil {
		t.Fatal("expected error for a directory")
	}
}

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", ComposeFileName} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nested := filepath.Join(root, "internal", "deploy")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Fatalf("got %q want %q", got, root)
	}
}

func TestFindRepoRootMissing(t *testing.T) {
	if _, err := FindRepoRoot(t.TempDir()); err == nil {
		t.Fatal("expected error without a compose file")
	}
}
