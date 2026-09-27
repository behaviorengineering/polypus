package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

// DefaultConfigDir returns the machine-wide Polypus config directory.
// Honors XDG_CONFIG_HOME when set; otherwise ~/.config/polypus.
func DefaultConfigDir() (string, error) {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

// DefaultConfigPath returns ~/.config/polypus/config.yaml (or $XDG_CONFIG_HOME/polypus/config.yaml).
func DefaultConfigPath() (string, error) {
	dir, err := DefaultConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

// DefaultCacheDir returns ~/.cache/polypus (or $XDG_CACHE_HOME/polypus).
func DefaultCacheDir() (string, error) {
	return xdgDir("XDG_CACHE_HOME", ".cache")
}

// DefaultSwitchyardRoutesPath returns the generated Switchyard TOML path.
func DefaultSwitchyardRoutesPath() (string, error) {
	dir, err := DefaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "switchyard", "routes.toml"), nil
}

// DefaultModelsInventoryCachePath returns the default models inventory disk cache path.
func DefaultModelsInventoryCachePath() (string, error) {
	dir, err := DefaultCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "models-inventory.json"), nil
}

// ResolveModelsInventoryCachePath picks the inventory cache file.
// Order: POLYPUS_MODELS_CACHE, then ~/.cache/polypus/models-inventory.json.
func ResolveModelsInventoryCachePath() string {
	if path := strings.TrimSpace(os.Getenv("POLYPUS_MODELS_CACHE")); path != "" {
		return path
	}
	path, err := DefaultModelsInventoryCachePath()
	if err != nil {
		return ""
	}
	return path
}

// DefaultStateDir returns ~/.local/state/polypus (or $XDG_STATE_HOME/polypus).
func DefaultStateDir() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		return filepath.Join(xdg, "polypus"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "polypus"), nil
}

// DefaultProcessComposeSockPath returns the default process-compose Unix socket path.
func DefaultProcessComposeSockPath() (string, error) {
	dir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "process-compose.sock"), nil
}

// ResolveConfigPath picks the router config file.
// Order: POLYPUS_CONFIG, ~/.config/polypus/config.yaml, $POLYPUS_ROOT/config.yaml, ./config.yaml.
// Returns "" when none exist (caller may fall back to env-only defaults).
func ResolveConfigPath() string {
	path, _ := ResolveConfigPathWithError()
	return path
}

// ResolveConfigPathWithError is ResolveConfigPath but surfaces invalid override paths.
func ResolveConfigPathWithError() (string, error) {
	return operatorconfig.ResolveConfigPath(operatorConfigOptions())
}

func xdgDir(envName, homeSubdir string) (string, error) {
	if xdg := strings.TrimSpace(os.Getenv(envName)); xdg != "" {
		return filepath.Join(xdg, "polypus"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, homeSubdir, "polypus"), nil
}
