// Package paths resolves the host paths a deploy bind-mounts into its containers,
// in the form Docker accepts on each operating system.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// ResolveGatewayConfig returns the absolute host path of the gateway YAML to
// bind-mount as the Polypus config. The path is required: the environment that
// is deployed names its gateway YAML, not this repository.
func ResolveGatewayConfig(path string) (string, error) {
	const op = "paths.ResolveGatewayConfig"
	path = strings.TrimSpace(path)
	if path == "" {
		return "", derrors.New(derrors.CodeInvalid, op, "gateway config required (gateway: in the environment config)")
	}
	dest, err := filepath.Abs(path)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInvalid, op, "gateway config "+path)
	}
	info, err := os.Stat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return "", derrors.New(derrors.CodeInvalid, op, "gateway config not found: "+dest)
		}
		return "", derrors.Wrap(err, derrors.CodeInvalid, op, "stat "+dest)
	}
	if info.IsDir() {
		return "", derrors.New(derrors.CodeInvalid, op, "gateway config must be a file, not a directory: "+dest)
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
