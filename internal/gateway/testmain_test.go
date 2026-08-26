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
	defer os.RemoveAll(dir)
	_ = os.Unsetenv("POLYPUS_CONFIG")
	_ = os.Unsetenv("POLYPUS_ROOT")
	_ = os.Setenv("XDG_CONFIG_HOME", dir)
	os.Exit(m.Run())
}
