package images

import (
	"fmt"
	"os"
	"strings"
)

// Verify checks images.env for the pins svc needs, and rejects :latest on any pin.
func Verify(path string, svc Services) error {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator's own images file
	if err != nil {
		return fmt.Errorf("images: read %q: %w", path, err)
	}
	return VerifyPins(string(data), path, svc)
}

// VerifyPins checks pins (images.env content; path names it in errors) for the
// pins svc needs, and rejects :latest on any pin.
func VerifyPins(pins, path string, svc Services) error {
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
	for _, key := range svc.pinKeys() {
		if _, ok := found[key]; !ok {
			return fmt.Errorf("images: missing %s in %s", key, path)
		}
	}
	for key, val := range found {
		if strings.HasSuffix(val, ":latest") {
			return fmt.Errorf("images: %s must not use :latest (got %s)", key, val)
		}
	}
	return nil
}
