package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

// ComposeFileName is the deploy stack at the root of a Polypus checkout.
const ComposeFileName = "docker-compose.deploy.yml"

// FindRepoRoot walks up from start to the Polypus checkout that holds the deploy
// stack, so the deploy finds its compose file, collector config and image pins.
func FindRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("paths: repo root from %q: %w", start, err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ComposeFileName)); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("paths: no Polypus checkout with %s above %s (pass --root)", ComposeFileName, start)
		}
		dir = parent
	}
}
