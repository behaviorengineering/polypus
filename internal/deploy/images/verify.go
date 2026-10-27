package images

import (
	"fmt"
	"os"
	"strings"
)

var requiredKeys = []string{
	polypusImageKey,
	switchyardImageKey,
	"PHOENIX_IMAGE",
	"HYPERDX_IMAGE",
	"TAILSCALE_IMAGE",
	"OTELCOL_IMAGE",
}

// Verify checks images.env for required pins and rejects :latest.
func Verify(path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator's own images file
	if err != nil {
		return fmt.Errorf("images: read %q: %w", path, err)
	}
	return VerifyPins(string(data), path)
}

// VerifyPins checks pins (images.env content; path names it in errors) for
// required pins and rejects :latest.
func VerifyPins(pins, path string) error {
	lines := strings.Split(pins, "\n")
	found := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		found[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	for _, key := range requiredKeys {
		val, ok := found[key]
		if !ok {
			return fmt.Errorf("images: missing %s in %s", key, path)
		}
		if strings.HasSuffix(val, ":latest") {
			return fmt.Errorf("images: %s must not use :latest (got %s)", key, val)
		}
	}
	return nil
}
