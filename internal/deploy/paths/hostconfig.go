package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveGatewayConfig returns the absolute host path of the gateway YAML to
// bind-mount as the Polypus config. The path is required: the environment that
// is deployed names its gateway YAML, not this repository.
func ResolveGatewayConfig(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("paths: gateway config required (gateway: in the environment config)")
	}
	dest, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("paths: gateway config %q: %w", path, err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("paths: gateway config not found: %s", dest)
		}
		return "", fmt.Errorf("paths: stat %q: %w", dest, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("paths: gateway config must be a file, not a directory: %s", dest)
	}
	return dest, nil
}

// DockerBindPath normalizes a host path for compose volume env on Windows.
func DockerBindPath(p string) string {
	if runtime.GOOS == "windows" {
		return filepath.ToSlash(p)
	}
	return p
}
