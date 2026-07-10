package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// BackendModels controls inventory sync and enablement for one backend.
type BackendModels struct {
	// Sync pulls upstream inventory when true (default). When false, list only allow entries.
	Sync *bool `yaml:"sync"`
	// AllowConfigured is true when the allow key was present in YAML (including empty list).
	AllowConfigured bool `yaml:"-"`
	// Allow is the enable list (bare or backend_id/… ids).
	Allow []string `yaml:"-"`
}

// UnmarshalYAML distinguishes missing allow from allow: [].
func (m *BackendModels) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		Sync  *bool     `yaml:"sync"`
		Allow *[]string `yaml:"allow"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	m.Sync = raw.Sync
	if raw.Allow != nil {
		m.AllowConfigured = true
		m.Allow = append([]string(nil), (*raw.Allow)...)
	}
	return nil
}

// ShouldSync reports whether upstream /v1/models should be queried.
func (m *BackendModels) ShouldSync() bool {
	if m == nil || m.Sync == nil {
		return true
	}
	return *m.Sync
}

// HasAllowGate reports whether models.allow is set for this backend.
func (m *BackendModels) HasAllowGate() bool {
	return m != nil && m.AllowConfigured
}

// NormalizeDownstream strips a known backend_id/ prefix for allow matching.
func NormalizeDownstream(backendID, model string) string {
	backendID = strings.TrimSpace(backendID)
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if backendID != "" && strings.HasPrefix(model, backendID+"/") {
		return strings.TrimSpace(model[len(backendID)+1:])
	}
	return model
}

// IsModelAllowed reports whether model may be listed or used for backendID.
// When the backend has no allow gate, always true.
func (b BackendDef) IsModelAllowed(model string) bool {
	if b.Models == nil || !b.Models.HasAllowGate() {
		return true
	}
	return ModelInAllowList(b.ID, model, b.Models.Allow)
}

// ModelInAllowList matches model against allow entries (bare or prefixed).
func ModelInAllowList(backendID, model string, allow []string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	down := NormalizeDownstream(backendID, model)
	for _, entry := range allow {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		entryDown := NormalizeDownstream(backendID, entry)
		if model == entry || model == entryDown || down == entry || down == entryDown {
			return true
		}
		// Prefixed public id vs bare entry.
		if backendID != "" {
			if model == backendID+"/"+entryDown || model == backendID+"/"+entry {
				return true
			}
			if entry == backendID+"/"+down {
				return true
			}
		}
	}
	return false
}

// SyntheticAllowModels builds OpenAI-style public ids from allow when sync is off or as fill-in.
func SyntheticAllowModels(backendID string, allow []string) []string {
	out := make([]string, 0, len(allow))
	seen := make(map[string]struct{})
	for _, entry := range allow {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		down := NormalizeDownstream(backendID, entry)
		if down == "" {
			continue
		}
		id := backendID + "/" + down
		if backendID == "" {
			id = down
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// Validate models config is well-formed (optional fields).
func (m *BackendModels) validate(backendID string) error {
	if m == nil {
		return nil
	}
	if m.AllowConfigured {
		for i, a := range m.Allow {
			if strings.TrimSpace(a) == "" {
				return fmt.Errorf("router: backends.%s.models.allow[%d] empty", backendID, i)
			}
		}
	}
	return nil
}
