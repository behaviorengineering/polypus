package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
)

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
func ResolveModelsInventoryCachePath() (string, error) {
	out, err := resolveModelsInventoryCachePath()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "config.ResolveModelsInventoryCachePath", "cache dir")
	}
	return out, nil
}

// resolveModelsInventoryCachePath is ResolveModelsInventoryCachePath before its error is typed at the package boundary.
func resolveModelsInventoryCachePath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("POLYPUS_MODELS_CACHE")); path != "" {
		return path, nil
	}
	return DefaultModelsInventoryCachePath()
}

// DefaultStateDir returns ~/.local/state/polypus (or $XDG_STATE_HOME/polypus).
func DefaultStateDir() (string, error) {
	return xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// defaultBatchDir returns ~/.local/state/polypus/batch (or $XDG_STATE_HOME/polypus/batch).
func defaultBatchDir() (string, error) {
	dir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "batch"), nil
}

// ResolveBatchDir picks the on-disk batch and files store root.
// Order: POLYPUS_BATCH_DIR, then DefaultBatchDir.
func ResolveBatchDir() (string, error) {
	out, err := resolveBatchDir()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "config.ResolveBatchDir", "state dir")
	}
	return out, nil
}

// resolveBatchDir is ResolveBatchDir before its error is typed at the package boundary.
func resolveBatchDir() (string, error) {
	if path := strings.TrimSpace(os.Getenv("POLYPUS_BATCH_DIR")); path != "" {
		return path, nil
	}
	return defaultBatchDir()
}

// DefaultProcessComposeSockPath returns the default process-compose Unix socket path.
func DefaultProcessComposeSockPath() (string, error) {
	dir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "process-compose.sock"), nil
}

// ResolveConfigPathWithError picks the router config file.
// Order: POLYPUS_CONFIG, ~/.config/polypus/config.yaml, $POLYPUS_ROOT/config.yaml, ./config.yaml.
// Returns "" when none exist (caller may fall back to env-only defaults), and an
// error for an invalid override path.
func ResolveConfigPathWithError() (string, error) {
	out, err := resolveConfigPathWithError()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.ResolveConfigPathWithError", "config path")
	}
	return out, nil
}

// resolveConfigPathWithError is ResolveConfigPathWithError before its error is typed at the package boundary.
func resolveConfigPathWithError() (string, error) {
	return operatorconfig.ResolveConfigPath(operatorConfigOptions())
}

// xdgDir is $envName/polypus when the XDG variable is set, else ~/homeSubdir/polypus.
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

// ResolveAdminKeysPath picks the admin API key store path.
// Order: POLYPUS_ADMIN_KEYS, then $XDG_STATE_HOME/polypus/admin-api-keys.json.
func ResolveAdminKeysPath() (string, error) {
	out, err := resolveAdminKeysPath()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "config.ResolveAdminKeysPath", "state dir")
	}
	return out, nil
}

// resolveAdminKeysPath is ResolveAdminKeysPath before its error is typed at the package boundary.
func resolveAdminKeysPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("POLYPUS_ADMIN_KEYS")); path != "" {
		return path, nil
	}
	dir, err := DefaultStateDir()
	if err != nil {
		return "", fmt.Errorf("admin keys path: %w", err)
	}
	return filepath.Join(dir, "admin-api-keys.json"), nil
}
