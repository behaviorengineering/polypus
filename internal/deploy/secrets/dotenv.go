package secrets

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// NonEmptyDotenvKeys reads a dotenv file and returns env names with non-empty values.
// Missing file yields an empty map (no error).
func NonEmptyDotenvKeys(path string) (map[string]bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]bool{}, nil
	}
	f, err := os.Open(path) //nolint:gosec // G304: operator-supplied deploy env path
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("secrets: read dotenv %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	out := make(map[string]bool)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseDotenvLine(line)
		if !ok || key == "" {
			continue
		}
		if strings.TrimSpace(unquoteDotenv(val)) != "" {
			out[key] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("secrets: scan dotenv %q: %w", path, err)
	}
	return out, nil
}

func parseDotenvLine(line string) (key, val string, ok bool) {
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	i := strings.Index(line, "=")
	if i <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), line[i+1:], true
}

func unquoteDotenv(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}
