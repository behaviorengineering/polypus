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
// LoadProcessFlagsFrom reads process toggles from the config at path, or from the
// resolved config when path is empty.
func LoadProcessFlagsFrom(path string) (ProcessFlags, error) {
	if strings.TrimSpace(path) == "" {
		return LoadProcessFlags()
	}
	out, err := readProcessFlags(path)
	if err != nil {
		return ProcessFlags{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.LoadProcessFlagsFrom", "process flags")
	}
	return out, nil
}

func loadProcessFlags() (ProcessFlags, error) {
	path, err := ResolveConfigPathWithError()
	if err != nil {
		return ProcessFlags{}, fmt.Errorf("process flags: %w", err)
	}
	return readProcessFlags(path)
}

// readProcessFlags reads the processes block of the config at path; no path means
// no flags.
func readProcessFlags(path string) (ProcessFlags, error) {
	if path == "" {
		return ProcessFlags{}, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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

// ParseEnvBool reads a boolean env var: fallback when unset, an error when set to a
// word ParseBoolishEnv does not know.
func ParseEnvBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	if v, ok := ParseBoolishEnv(raw); ok {
		return v, nil
	}
	return fallback, derrors.New(derrors.CodeInvalid, "config.ParseEnvBool", "not a boolean (use 1/0, true/false, yes/no or on/off)").
		With("env", key).With("value", raw)
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
