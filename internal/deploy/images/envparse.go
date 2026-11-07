package images

import (
	"os"
	"strings"

	"github.com/behaviorengineering/polypus/internal/clients/registry"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// parseEnv reads KEY=VALUE lines of an images file, skipping blanks and comments.
// Image pins are never quoted, so values are kept as written.
func parseEnv(data string) map[string]string {
	found := make(map[string]string)
	for _, line := range strings.Split(data, "\n") {
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
	return found
}

func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator's own images file
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, "images.readEnvFile", "read "+path)
	}
	return parseEnv(string(data)), nil
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
			return nil, derrors.New(derrors.CodeInvalid, "images.probeRefs", "missing "+key+" in "+path)
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
		return "", derrors.New(derrors.CodeInvalid, "images.versionFromRefs", "no image refs")
	}
	ref, err := registry.ParseRef(refs[0])
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeInvalid, "images.versionFromRefs", "parse "+refs[0])
	}
	return ref.Tag, nil
}
