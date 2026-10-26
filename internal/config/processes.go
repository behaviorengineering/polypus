package config

import (
	"bytes"
	"fmt"
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

// MLXEnabled reports whether the MLX process should run.
// When processes.mlx is omitted, fallback is returned.
func (p ProcessFlags) MLXEnabled(fallback bool) bool {
	if p.MLX != nil {
		return *p.MLX
	}
	return fallback
}

// MLXSet reports whether processes.mlx was present in config.
func (p ProcessFlags) MLXSet() bool {
	return p.MLX != nil
}

// LoadProcessFlags reads processes.* from the resolved router config file.
// Missing config file yields zero ProcessFlags (all keys unset).
func LoadProcessFlags() (ProcessFlags, error) {
	out, err := loadProcessFlags()
	if err != nil {
		return ProcessFlags{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.LoadProcessFlags", "process flags")
	}
	return out, nil
}

// loadProcessFlags is LoadProcessFlags before its error is typed at the package boundary.
func loadProcessFlags() (ProcessFlags, error) {
	path, err := ResolveConfigPathWithError()
	if err != nil {
		return ProcessFlags{}, fmt.Errorf("process flags: %w", err)
	}
	if path == "" {
		return ProcessFlags{}, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		if os.IsNotExist(err) {
			return ProcessFlags{}, nil
		}
		return ProcessFlags{}, fmt.Errorf("process flags %s: %w", path, err)
	}
	var file struct {
		Processes processesFile `yaml:"processes"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// Intentionally not KnownFields: this helper only needs processes.*.
	if err := dec.Decode(&file); err != nil {
		return ProcessFlags{}, fmt.Errorf("process flags %s: %w", path, err)
	}
	return ProcessFlags{MLX: file.Processes.MLX}, nil
}

// EnvBool reads a boolean env var with ParseBoolishEnv, returning fallback when it is
// unset or not a recognised word.
func EnvBool(key string, fallback bool) bool {
	if v, ok := ParseBoolishEnv(os.Getenv(key)); ok {
		return v
	}
	return fallback
}

// ParseBoolishEnv maps common truthy/falsey env values. Empty returns ok=false.
func ParseBoolishEnv(raw string) (value bool, ok bool) {
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
