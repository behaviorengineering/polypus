// Package stack holds the deploy's Docker Compose stack, embedded in the binary so a
// release binary deploys without a Polypus checkout. otelcol.config.yaml and
// images.env are copies of the repository root's (shared with local serve); a test
// fails when they drift, and go tool task sync-deploy-stack refreshes them.
package stack

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

// ComposeFileName is the stack's compose file, written into each environment's folder.
const ComposeFileName = "docker-compose.deploy.yml"

//go:embed docker-compose.deploy.yml
var composeFile []byte

//go:embed hyperdx.custom.config.yaml
var hyperdxConfig []byte

//go:embed otelcol.config.yaml
var otelcolConfig []byte

//go:embed images.env
var imagePins []byte

// files are written beside each other: the compose file mounts the two configs by
// relative path.
var files = map[string][]byte{
	ComposeFileName:              composeFile,
	"hyperdx.custom.config.yaml": hyperdxConfig,
	"otelcol.config.yaml":        otelcolConfig,
}

// ImagePins returns the stack's images.env: the observability and Tailscale pins
// the deploy adds the Polypus and Switchyard release images to.
func ImagePins() []byte {
	return imagePins
}

// Write writes the stack into dir (created if missing) and returns the compose
// file's path. It overwrites what an earlier deploy wrote there.
func Write(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("stack: mkdir %q: %w", dir, err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", fmt.Errorf("stack: write %s: %w", name, err)
		}
	}
	return filepath.Join(dir, ComposeFileName), nil
}
