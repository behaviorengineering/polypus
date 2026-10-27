package routers

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var routersLine = regexp.MustCompile(`^\s*routers\s*:`)

// HasTopLevelRouters reports whether config has an uncommented top-level routers: key.
func HasTopLevelRouters(path string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the environment's own gateway YAML.
	if err != nil {
		return false, fmt.Errorf("routers: read %q: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if routersLine.MatchString(line) {
			return true, nil
		}
	}
	return false, nil
}
