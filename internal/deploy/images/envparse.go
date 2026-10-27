package images

import (
	"fmt"
	"os"
	"strings"
)

func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator's own images file
	if err != nil {
		return nil, fmt.Errorf("images: read %q: %w", path, err)
	}
	found := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
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
	return found, nil
}

func probeRefsFromEnv(path string, keys []string) ([]string, error) {
	found, err := readEnvFile(path)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, key := range keys {
		val, ok := found[key]
		if !ok || val == "" {
			return nil, fmt.Errorf("images: missing %s in %s", key, path)
		}
		refs = append(refs, val)
	}
	return refs, nil
}

// PolypusVersionFromEnv returns the semver tag from POLYPUS_IMAGE in images.env.
func PolypusVersionFromEnv(path string) (string, error) {
	refs, err := probeRefsFromEnv(path, []string{polypusImageKey})
	if err != nil {
		return "", err
	}
	return versionFromRefs(refs)
}

func versionFromRefs(refs []string) (string, error) {
	if len(refs) == 0 {
		return "", fmt.Errorf("images: no image refs")
	}
	_, tag, err := splitImageRef(refs[0])
	if err != nil {
		return "", err
	}
	return tag, nil
}
