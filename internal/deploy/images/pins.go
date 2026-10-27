package images

import (
	"fmt"
	"os"
	"strings"
)

const (
	polypusImageKey     = "POLYPUS_IMAGE"
	switchyardImageKey  = "SWITCHYARD_IMAGE"
	polypusImageRepo    = "ghcr.io/behaviorengineering/polypus"
	switchyardImageRepo = "ghcr.io/behaviorengineering/polypus-switchyard"
)

// VersionPins returns the image pins for a deploy of release version: the Polypus
// and Switchyard images of that release, then every other pin in repoPins (the
// repository's images.env). A build without a release version ("dev") has no
// images of its own, so the caller passes a pinned images file instead.
func VersionPins(repoPins, version string) (string, error) {
	tag, err := ParseImageTag(version)
	if err != nil {
		return "", fmt.Errorf("images: build version %q has no release images; pass --images with pinned images: %w", version, err)
	}
	data, err := os.ReadFile(repoPins) //nolint:gosec // G304: the repository's own images.env
	if err != nil {
		return "", fmt.Errorf("images: read %q: %w", repoPins, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by polypus deploy for release %s. Do not edit.\n", tag.App)
	fmt.Fprintf(&b, "%s=%s:%s\n", polypusImageKey, polypusImageRepo, tag.App)
	fmt.Fprintf(&b, "%s=%s:%s\n", switchyardImageKey, switchyardImageRepo, tag.App)
	for _, line := range strings.Split(string(data), "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") || key == polypusImageKey || key == switchyardImageKey {
			continue
		}
		b.WriteString(strings.TrimSpace(line))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// WritePins writes pins (from VersionPins) to path.
func WritePins(path, pins string) error {
	if err := os.WriteFile(path, []byte(pins), 0o600); err != nil {
		return fmt.Errorf("images: write %q: %w", path, err)
	}
	return nil
}
