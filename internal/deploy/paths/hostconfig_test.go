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
