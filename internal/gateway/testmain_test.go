package gateway

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates machine-wide config and caches so unit tests neither load
// ~/.config/polypus nor write ~/.cache/polypus.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "polypus-gateway-config-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = os.Unsetenv("POLYPUS_CONFIG")
	_ = os.Unsetenv("POLYPUS_ROOT")
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	if err := isolateCaches(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// isolateCaches points the cache directory under dir, and the models inventory disk
// cache at a path that cannot exist, so every handler a test builds starts with an
// empty, memory-only inventory cache: GET /v1/models serves the cache first, and a
// list another test left on disk would answer for this test's backends. Tests that
// exercise the disk cache set POLYPUS_MODELS_CACHE themselves.
func isolateCaches(dir string) error {
	if err := os.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache")); err != nil {
		return err
	}
	blocker := filepath.Join(dir, "no-models-cache")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		return err
	}
	return os.Setenv("POLYPUS_MODELS_CACHE", filepath.Join(blocker, "models-inventory.json"))
}
