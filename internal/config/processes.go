package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"gopkg.in/yaml.v3"
)

// ProcessFlags holds optional process-compose toggles from config.
// A nil pointer means the key was omitted (caller applies its own fallback).
type ProcessFlags struct {
	MLX *bool
}

type processesFile struct {
	MLX *bool `yaml:"mlx"`
}

// MLXSet reports whether processes.mlx was present in config.
func (p ProcessFlags) MLXSet() bool {
	return p.MLX != nil
}

// LoadProcessFlagsFrom reads process toggles from the config at path, which must
// exist, or from the resolved config when path is empty. When discovery finds no
// config, every key is unset.
func LoadProcessFlagsFrom(path string) (ProcessFlags, error) {
	out, err := loadProcessFlags(path)
	if err != nil {
		return ProcessFlags{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.LoadProcessFlagsFrom", "process flags")
	}
	return out, nil
}

// loadProcessFlags is LoadProcessFlagsFrom before its error is typed at the
// package boundary. The wrap there names "process flags", so this adds no prefix.
func loadProcessFlags(path string) (ProcessFlags, error) {
	if p := strings.TrimSpace(path); p != "" {
		return readProcessFlags(p)
	}
	resolved, err := resolveConfigPath()
	if err != nil {
		return ProcessFlags{}, err
	}
	if resolved == "" {
		return ProcessFlags{}, nil
	}
	return readProcessFlags(resolved)
}

// readProcessFlags reads the processes block of the config at path.
func readProcessFlags(path string) (ProcessFlags, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ProcessFlags{}, derrors.New(derrors.CodeInvalid, "config.readProcessFlags",
				fmt.Sprintf("config file %s not found", path)).With("path", path)
		}
		return ProcessFlags{}, fmt.Errorf("%s: %w", path, err)
	}
	var file struct {
		Processes processesFile `yaml:"processes"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// Intentionally not KnownFields: this helper only needs processes.*.
	if err := dec.Decode(&file); err != nil {
		return ProcessFlags{}, fmt.Errorf("%s: %w", path, err)
	}
	return ProcessFlags{MLX: file.Processes.MLX}, nil
}

// ParseEnvBool reads a boolean env var: fallback when unset, an error when set to a
// word parseBoolishEnv does not know.
func ParseEnvBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	if v, ok := parseBoolishEnv(raw); ok {
		return v, nil
	}
	return fallback, derrors.New(derrors.CodeInvalid, "config.ParseEnvBool", key+" is not a boolean (use 1, true, yes, on, 0, false, no or off)").
		With("env", key).With("value", raw)
}

// parseBoolishEnv maps common truthy/falsey env values. Empty returns ok=false.
func parseBoolishEnv(raw string) (value bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return false, false
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}
