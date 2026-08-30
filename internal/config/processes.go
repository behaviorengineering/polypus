package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

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
	path := ResolveConfigPath()
	if path == "" {
		return ProcessFlags{}, nil
	}
	raw, err := os.ReadFile(path)
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
