package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Capability is a speech backend feature set.
type Capability string

const (
	CapChat    Capability = "chat"
	CapVision  Capability = "vision"
	CapEmbed   Capability = "embed"
	CapTTS     Capability = "tts"
	CapSTT     Capability = "stt"
	CapVoices  Capability = "voices"
)

// BackendDef is one OpenAI-compatible speech worker.
type BackendDef struct {
	ID           string         `yaml:"-"`
	BaseURL      string         `yaml:"base_url"`
	Capabilities []Capability   `yaml:"capabilities"`
	Models       *BackendModels `yaml:"models"`
}

// RouterConfig holds multi-backend routing for the Polypus gateway.
type RouterConfig struct {
	DefaultEmbedBackend  string                `yaml:"default_embed_backend"`
	DefaultChatBackend   string                `yaml:"default_chat_backend"`
	DefaultVisionBackend string                `yaml:"default_vision_backend"`
	DefaultTTSBackend    string                `yaml:"default_tts_backend"`
	DefaultSTTBackend    string                `yaml:"default_stt_backend"`
	DefaultProxyBackend  string                `yaml:"default_proxy_backend"`
	Backends             map[string]BackendDef `yaml:"backends"`
}

// HasCapability reports whether the backend supports a capability.
func (b BackendDef) HasCapability(cap Capability) bool {
	for _, c := range b.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

type routerFile struct {
	DefaultEmbedBackend  string                      `yaml:"default_embed_backend"`
	DefaultChatBackend   string                      `yaml:"default_chat_backend"`
	DefaultVisionBackend string                      `yaml:"default_vision_backend"`
	DefaultTTSBackend    string                      `yaml:"default_tts_backend"`
	DefaultSTTBackend    string                      `yaml:"default_stt_backend"`
	DefaultProxyBackend  string                      `yaml:"default_proxy_backend"`
	Backends             map[string]backendFileEntry `yaml:"backends"`
}

type backendFileEntry struct {
	BaseURL      string         `yaml:"base_url"`
	Capabilities []string       `yaml:"capabilities"`
	Models       *BackendModels `yaml:"models"`
}

// LoadRouterConfig builds routing from config file and POLYPUS_* env.
func LoadRouterConfig(opts ServeOptions) (RouterConfig, error) {
	cfg, fromFile, err := loadRouterFile(opts)
	if err != nil {
		return RouterConfig{}, err
	}
	if !fromFile || len(cfg.Backends) == 0 {
		cfg = defaultRouterFromEnv(opts)
	}
	applyRouterEnvOverrides(&cfg, opts)
	if err := normalizeRouterConfig(&cfg); err != nil {
		return RouterConfig{}, err
	}
	return cfg, nil
}

func loadRouterFile(opts ServeOptions) (RouterConfig, bool, error) {
	path := strings.TrimSpace(os.Getenv("POLYPUS_CONFIG"))
	if path == "" {
		if root := strings.TrimSpace(os.Getenv("POLYPUS_ROOT")); root != "" {
			path = filepath.Join(root, "config.yaml")
		} else if _, err := os.Stat("config.yaml"); err == nil {
			path = "config.yaml"
		}
	}
	if path == "" {
		return RouterConfig{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return RouterConfig{}, false, nil
		}
		return RouterConfig{}, false, fmt.Errorf("router config %s: %w", path, err)
	}
	var file routerFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return RouterConfig{}, false, fmt.Errorf("router config %s: %w", path, err)
	}
	cfg := RouterConfig{
		DefaultEmbedBackend:  strings.TrimSpace(file.DefaultEmbedBackend),
		DefaultChatBackend:   strings.TrimSpace(file.DefaultChatBackend),
		DefaultVisionBackend: strings.TrimSpace(file.DefaultVisionBackend),
		DefaultTTSBackend:    strings.TrimSpace(file.DefaultTTSBackend),
		DefaultSTTBackend:    strings.TrimSpace(file.DefaultSTTBackend),
		DefaultProxyBackend:  strings.TrimSpace(file.DefaultProxyBackend),
		Backends:             make(map[string]BackendDef, len(file.Backends)),
	}
	for id, entry := range file.Backends {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		caps := make([]Capability, 0, len(entry.Capabilities))
		for _, c := range entry.Capabilities {
			caps = append(caps, Capability(strings.TrimSpace(c)))
		}
		cfg.Backends[id] = BackendDef{
			ID:           id,
			BaseURL:      strings.TrimRight(strings.TrimSpace(entry.BaseURL), "/"),
			Capabilities: caps,
			Models:       entry.Models,
		}
	}
	return cfg, true, nil
}

func defaultRouterFromEnv(opts ServeOptions) RouterConfig {
	backend := strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
	return RouterConfig{
		DefaultTTSBackend:   "mlx_local",
		DefaultSTTBackend:   "mlx_local",
		DefaultProxyBackend: "mlx_local",
		Backends: map[string]BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      backend,
				Capabilities: []Capability{CapTTS, CapSTT, CapVoices},
			},
		},
	}
}

func applyRouterEnvOverrides(cfg *RouterConfig, opts ServeOptions) {
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_EMBED_BACKEND")); v != "" {
		cfg.DefaultEmbedBackend = v
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_CHAT_BACKEND")); v != "" {
		cfg.DefaultChatBackend = v
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_VISION_BACKEND")); v != "" {
		cfg.DefaultVisionBackend = v
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_TTS_BACKEND")); v != "" {
		cfg.DefaultTTSBackend = v
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_STT_BACKEND")); v != "" {
		cfg.DefaultSTTBackend = v
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_PROXY_BACKEND")); v != "" {
		cfg.DefaultProxyBackend = v
	}
	// CLI --backend overrides mlx_local URL when present.
	if opts.BackendURL != "" {
		if b, ok := cfg.Backends["mlx_local"]; ok {
			b.BaseURL = strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
			cfg.Backends["mlx_local"] = b
		}
	}
}

func normalizeRouterConfig(cfg *RouterConfig) error {
	if len(cfg.Backends) == 0 {
		return fmt.Errorf("router: no backends configured")
	}
	if cfg.DefaultTTSBackend == "" {
		cfg.DefaultTTSBackend = "mlx_local"
	}
	if cfg.DefaultSTTBackend == "" {
		cfg.DefaultSTTBackend = cfg.DefaultTTSBackend
	}
	if cfg.DefaultProxyBackend == "" {
		cfg.DefaultProxyBackend = cfg.DefaultTTSBackend
	}
	for id, b := range cfg.Backends {
		if b.ID == "" {
			b.ID = id
		}
		if b.BaseURL == "" {
			return fmt.Errorf("router: backends.%s.base_url required", id)
		}
		if len(b.Capabilities) == 0 {
			return fmt.Errorf("router: backends.%s.capabilities required", id)
		}
		if err := b.Models.validate(id); err != nil {
			return err
		}
		cfg.Backends[id] = b
	}
	if err := requireBackend(cfg, cfg.DefaultTTSBackend, CapTTS, "default_tts_backend"); err != nil {
		return err
	}
	if err := requireBackend(cfg, cfg.DefaultSTTBackend, CapSTT, "default_stt_backend"); err != nil {
		return err
	}
	if err := requireBackend(cfg, cfg.DefaultProxyBackend, CapVoices, "default_proxy_backend"); err != nil {
		return err
	}
	if cfg.DefaultEmbedBackend != "" {
		if err := requireBackend(cfg, cfg.DefaultEmbedBackend, CapEmbed, "default_embed_backend"); err != nil {
			return err
		}
	}
	if cfg.DefaultChatBackend != "" {
		if err := requireBackend(cfg, cfg.DefaultChatBackend, CapChat, "default_chat_backend"); err != nil {
			return err
		}
	}
	if cfg.DefaultVisionBackend != "" {
		if err := requireBackend(cfg, cfg.DefaultVisionBackend, CapVision, "default_vision_backend"); err != nil {
			return err
		}
	}
	return nil
}

func requireBackend(cfg *RouterConfig, id string, cap Capability, field string) error {
	b, ok := cfg.Backends[id]
	if !ok {
		return fmt.Errorf("router: %s %q not found in backends", field, id)
	}
	if !b.HasCapability(cap) {
		return fmt.Errorf("router: %s %q lacks capability %s", field, id, cap)
	}
	return nil
}

// ProxyBackendURL returns the base URL for non-routed paths (e.g. /v1/audio/voices).
func (c RouterConfig) ProxyBackendURL() string {
	if b, ok := c.Backends[c.DefaultProxyBackend]; ok {
		return b.BaseURL
	}
	for _, b := range c.Backends {
		if b.HasCapability(CapVoices) {
			return b.BaseURL
		}
	}
	for _, b := range c.Backends {
		return b.BaseURL
	}
	return ""
}

// BackendIDs returns sorted backend ids for stable health output.
func (c RouterConfig) BackendIDs() []string {
	ids := make([]string, 0, len(c.Backends))
	for id := range c.Backends {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

func sortStrings(ss []string) {
	for i := 0; i < len(ss); i++ {
		for j := i + 1; j < len(ss); j++ {
			if ss[j] < ss[i] {
				ss[i], ss[j] = ss[j], ss[i]
			}
		}
	}
}
