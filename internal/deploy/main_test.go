package deploy

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points XDG_CONFIG_HOME at an empty directory, so a Resolver with no
// SecretFile never decrypts the operator's real ~/.config/<app>/secrets.enc.yaml.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "deploy-test-xdg-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.Setenv("XDG_CONFIG_HOME", dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}
