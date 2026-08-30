package gateway

import (
	"fmt"
	"os"
	"testing"
)

// TestMain isolates machine-wide config so unit tests do not load ~/.config/polypus.
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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
