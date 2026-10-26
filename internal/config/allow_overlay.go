package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/statefile"
	"gopkg.in/yaml.v3"
)

const modelsAllowOverlayFile = "models-allow-overlay.yaml"

// AllowOverlayFile is the on-disk shape for runtime model allow extras.
type AllowOverlayFile struct {
	Backends map[string]AllowOverlayBackend `yaml:"backends"`
}

// AllowOverlayBackend lists extra downstream model ids for one backend.
type AllowOverlayBackend struct {
	Allow []string `yaml:"allow"`
}

// ResolveModelsAllowOverlayPath picks the overlay file path.
// Order: POLYPUS_MODELS_ALLOW_OVERLAY, then $XDG_STATE_HOME/polypus/models-allow-overlay.yaml.
func ResolveModelsAllowOverlayPath() (string, error) {
	out, err := resolveModelsAllowOverlayPath()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "config.ResolveModelsAllowOverlayPath", "state dir")
	}
	return out, nil
}

// resolveModelsAllowOverlayPath is ResolveModelsAllowOverlayPath before its error is typed at the package boundary.
func resolveModelsAllowOverlayPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("POLYPUS_MODELS_ALLOW_OVERLAY")); path != "" {
		return path, nil
	}
	dir, err := DefaultStateDir()
	if err != nil {
		return "", fmt.Errorf("models allow overlay path: %w", err)
	}
	return filepath.Join(dir, modelsAllowOverlayFile), nil
}

// LoadAllowOverlay reads overlay YAML. Missing file returns empty overlay, not an error.
func LoadAllowOverlay(path string) (AllowOverlayFile, error) {
	out, err := loadAllowOverlay(path)
	if err != nil {
		return AllowOverlayFile{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.LoadAllowOverlay", "allow overlay")
	}
	return out, nil
}

// loadAllowOverlay is LoadAllowOverlay before its error is typed at the package boundary.
func loadAllowOverlay(path string) (AllowOverlayFile, error) {
	if strings.TrimSpace(path) == "" {
		return AllowOverlayFile{}, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return AllowOverlayFile{}, nil
		}
		return AllowOverlayFile{}, derrors.Wrap(err, derrors.CodeInternal, "config.LoadAllowOverlay", "read")
	}
	var file AllowOverlayFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return AllowOverlayFile{}, derrors.Wrap(err, derrors.CodeInvalid, "config.LoadAllowOverlay", "parse").With("path", path)
	}
	if file.Backends == nil {
		file.Backends = make(map[string]AllowOverlayBackend)
	}
	return file, nil
}

// SkippedOverlay is an overlay backend ApplyAllowOverlay left out, with the reason.
type SkippedOverlay struct {
	Backend string
	Reason  string
}

// Reasons an overlay backend is skipped.
const (
	SkipUnknownBackend = "unknown backend"
	SkipNoAllowGate    = "backend has no models.allow gate"
)

// ApplyAllowOverlay unions overlay allow entries into rcfg for gated backends only,
// and returns the overlay backends it skipped so the caller can log them.
func ApplyAllowOverlay(rcfg *RouterConfig, path string) ([]SkippedOverlay, error) {
	skipped, err := applyAllowOverlay(rcfg, path)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.ApplyAllowOverlay", "apply allow overlay")
	}
	return skipped, nil
}

// applyAllowOverlay is ApplyAllowOverlay before its error is typed at the package boundary.
func applyAllowOverlay(rcfg *RouterConfig, path string) ([]SkippedOverlay, error) {
	if rcfg == nil {
		return nil, derrors.New(derrors.CodeInvalid, "config.ApplyAllowOverlay", "nil RouterConfig")
	}
	overlay, err := LoadAllowOverlay(path)
	if err != nil {
		return nil, err
	}
	var skipped []SkippedOverlay
	for _, backendID := range slices.Sorted(maps.Keys(overlay.Backends)) {
		ob := overlay.Backends[backendID]
		b, ok := rcfg.Backends[backendID]
		if !ok {
			skipped = append(skipped, SkippedOverlay{Backend: backendID, Reason: SkipUnknownBackend})
			continue
		}
		if b.Models == nil || !b.Models.HasAllowGate() {
			skipped = append(skipped, SkippedOverlay{Backend: backendID, Reason: SkipNoAllowGate})
			continue
		}
		for _, entry := range ob.Allow {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			if ModelInAllowList(backendID, entry, b.Models.Allow) {
				continue
			}
			b.Models.Allow = append(b.Models.Allow, NormalizeDownstream(backendID, entry))
		}
		rcfg.Backends[backendID] = b
	}
	return skipped, nil
}

// OverlayAdd appends a downstream model id to the overlay file (overlay-only extras).
func OverlayAdd(path, backendID, downstream string) error {
	if err := overlayAdd(path, backendID, downstream); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), "config.OverlayAdd", "overlay add")
	}
	return nil
}

// overlayAdd is OverlayAdd before its error is typed at the package boundary.
func overlayAdd(path, backendID, downstream string) error {
	backendID = strings.TrimSpace(backendID)
	downstream = strings.TrimSpace(NormalizeDownstream(backendID, downstream))
	if backendID == "" || downstream == "" {
		return derrors.New(derrors.CodeInvalid, "config.OverlayAdd", "backend and model required")
	}
	return statefile.WithFileLock(path, func() error {
		file, err := LoadAllowOverlay(path)
		if err != nil {
			return err
		}
		if file.Backends == nil {
			file.Backends = make(map[string]AllowOverlayBackend)
		}
		ob := file.Backends[backendID]
		for _, e := range ob.Allow {
			if NormalizeDownstream(backendID, e) == downstream {
				return nil
			}
		}
		ob.Allow = append(ob.Allow, downstream)
		file.Backends[backendID] = ob
		raw, err := yaml.Marshal(file)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, "config.OverlayAdd", "marshal")
		}
		return statefile.WriteAtomic(path, raw, 0o600)
	})
}

// OverlayRemove drops one downstream model id from the overlay file (no-op if missing).
func OverlayRemove(path, backendID, downstream string) error {
	if err := overlayRemove(path, backendID, downstream); err != nil {
		return derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), "config.OverlayRemove", "overlay remove")
	}
	return nil
}

// overlayRemove is OverlayRemove before its error is typed at the package boundary.
func overlayRemove(path, backendID, downstream string) error {
	backendID = strings.TrimSpace(backendID)
	downstream = strings.TrimSpace(NormalizeDownstream(backendID, downstream))
	if backendID == "" || downstream == "" {
		return derrors.New(derrors.CodeInvalid, "config.OverlayRemove", "backend and model required")
	}
	return statefile.WithFileLock(path, func() error {
		file, err := LoadAllowOverlay(path)
		if err != nil {
			return err
		}
		if file.Backends == nil {
			return nil
		}
		ob, ok := file.Backends[backendID]
		if !ok {
			return nil
		}
		var kept []string
		removed := false
		for _, e := range ob.Allow {
			if NormalizeDownstream(backendID, e) == downstream {
				removed = true
				continue
			}
			kept = append(kept, e)
		}
		if !removed {
			return nil
		}
		if len(kept) == 0 {
			delete(file.Backends, backendID)
		} else {
			ob.Allow = kept
			file.Backends[backendID] = ob
		}
		raw, err := yaml.Marshal(file)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, "config.OverlayRemove", "marshal")
		}
		return statefile.WriteAtomic(path, raw, 0o600)
	})
}

// CloneBackendAllowMap returns a copy of YAML allow lists per backend (after load, before overlay).
func CloneBackendAllowMap(rcfg RouterConfig) map[string][]string {
	out := make(map[string][]string)
	for id, b := range rcfg.Backends {
		if b.Models == nil || !b.Models.HasAllowGate() {
			continue
		}
		out[id] = append([]string(nil), b.Models.Allow...)
	}
	return out
}

// OverlayOnlyAllow returns allow entries present in current but not in yamlBaseline.
func OverlayOnlyAllow(yamlBaseline map[string][]string, backendID string, current []string) []string {
	base := yamlBaseline[backendID]
	var extras []string
	for _, c := range current {
		if !ModelInAllowList(backendID, c, base) {
			extras = append(extras, NormalizeDownstream(backendID, c))
		}
	}
	return extras
}
