package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Capability is a speech backend feature set.
type Capability string

const (
	CapChat   Capability = "chat"
	CapVision Capability = "vision"
	CapEmbed  Capability = "embed"
	CapTTS    Capability = "tts"
	CapSTT    Capability = "stt"
	CapVoices Capability = "voices"
)

// BackendDef is one OpenAI-compatible inference worker.
type BackendDef struct {
	ID           string         `yaml:"-"`
	Remote       bool           `yaml:"remote"`
	Extension    string         `yaml:"extension"`
	BaseURL      string         `yaml:"base_url"`
	Auth         BackendAuth    `yaml:"auth"`
	Capabilities []Capability   `yaml:"capabilities"`
	Models       *BackendModels `yaml:"models"`
}

// HasExtension reports whether the backend uses a named extension module.
func (b BackendDef) HasExtension(name string) bool {
	return strings.EqualFold(strings.TrimSpace(b.Extension), strings.TrimSpace(name))
}

// IsCloudflareExtension reports whether the backend uses the Cloudflare extension.
func (b BackendDef) IsCloudflareExtension() bool {
	return b.HasExtension(ExtensionCloudflare)
}

// CapabilityBackend is an optional capability default (chat, vision, embed, TTS, STT, proxy).
type CapabilityBackend struct {
	Enabled bool
	Default string
}

// RouterConfig holds multi-backend routing for the Polypus gateway.
type RouterConfig struct {
	Chat       CapabilityBackend     `yaml:"-"`
	Vision     CapabilityBackend     `yaml:"-"`
	Embed      CapabilityBackend     `yaml:"-"`
	TTS        CapabilityBackend     `yaml:"-"`
	STT        CapabilityBackend     `yaml:"-"`
	Proxy      CapabilityBackend     `yaml:"-"`
	Timeouts   Timeouts              `yaml:"-"`
	Policy     RouterPolicy          `yaml:"policy"`
	Backends   map[string]BackendDef `yaml:"backends"`
	Routers    map[string]NamedRouter
	Switchyard SwitchyardConfig
}

// EffectiveChatBackend returns the chat default when chat is enabled; otherwise empty.
func (c RouterConfig) EffectiveChatBackend() string {
	if !c.Chat.Enabled {
		return ""
	}
	return c.Chat.Default
}

// EffectiveVisionBackend returns the vision default when vision is enabled; otherwise empty.
func (c RouterConfig) EffectiveVisionBackend() string {
	if !c.Vision.Enabled {
		return ""
	}
	return c.Vision.Default
}

// EffectiveEmbedBackend returns the embed default when embed is enabled; otherwise empty.
func (c RouterConfig) EffectiveEmbedBackend() string {
	if !c.Embed.Enabled {
		return ""
	}
	return c.Embed.Default
}

// EffectiveTTSBackend returns the TTS default when TTS is enabled; otherwise empty.
func (c RouterConfig) EffectiveTTSBackend() string {
	if !c.TTS.Enabled {
		return ""
	}
	return c.TTS.Default
}

// EffectiveSTTBackend returns the STT default when STT is enabled; otherwise empty.
func (c RouterConfig) EffectiveSTTBackend() string {
	if !c.STT.Enabled {
		return ""
	}
	return c.STT.Default
}

// EffectiveProxyBackend returns the proxy/voices default when proxy is enabled; otherwise empty.
func (c RouterConfig) EffectiveProxyBackend() string {
	if !c.Proxy.Enabled {
		return ""
	}
	return c.Proxy.Default
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

type capabilityBackendFile struct {
	Enabled *bool  `yaml:"enabled"`
	Default string `yaml:"default"`
}

type routerFile struct {
	ChatBackend   capabilityBackendFile       `yaml:"chat_backend"`
	VisionBackend capabilityBackendFile       `yaml:"vision_backend"`
	EmbedBackend  capabilityBackendFile       `yaml:"embed_backend"`
	TTSBackend    capabilityBackendFile       `yaml:"tts_backend"`
	STTBackend    capabilityBackendFile       `yaml:"stt_backend"`
	ProxyBackend  capabilityBackendFile       `yaml:"proxy_backend"`
	Timeouts      timeoutsFile                `yaml:"timeouts"`
	Policy        routerPolicyFile            `yaml:"policy"`
	Processes     processesFile               `yaml:"processes"`
	Backends      map[string]backendFileEntry `yaml:"backends"`
	Switchyard    switchyardFile              `yaml:"switchyard"`
	Routers       map[string]namedRouterFile  `yaml:"routers"`
}

type backendFileEntry struct {
	Remote       bool           `yaml:"remote"`
	Extension    string         `yaml:"extension"`
	BaseURL      string         `yaml:"base_url"`
	Auth         BackendAuth    `yaml:"auth"`
	Capabilities []string       `yaml:"capabilities"`
	Models       *BackendModels `yaml:"models"`
}

func parseCapabilityBackend(file capabilityBackendFile) CapabilityBackend {
	enabled := false
	if file.Enabled != nil {
		enabled = *file.Enabled
	}
	return CapabilityBackend{
		Enabled: enabled,
		Default: strings.TrimSpace(file.Default),
	}
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
	if cfg.Timeouts.Max == 0 {
		cfg.Timeouts = DefaultTimeouts()
	}
	applyRouterEnvOverrides(&cfg, opts)
	applySwitchyardEnvOverrides(&cfg)
	if err := normalizeRouterConfig(&cfg); err != nil {
		return RouterConfig{}, err
	}
	return cfg, nil
}

func loadRouterFile(opts ServeOptions) (RouterConfig, bool, error) {
	path := ResolveConfigPath()
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
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return RouterConfig{}, false, fmt.Errorf("router config %s: %w", path, err)
	}
	timeouts, err := parseTimeoutsFile(file.Timeouts)
	if err != nil {
		return RouterConfig{}, false, fmt.Errorf("router config %s: %w", path, err)
	}
	routers, err := parseNamedRouters(file.Routers)
	if err != nil {
		return RouterConfig{}, false, fmt.Errorf("router config %s: %w", path, err)
	}
	cfg := RouterConfig{
		Chat:       parseCapabilityBackend(file.ChatBackend),
		Vision:     parseCapabilityBackend(file.VisionBackend),
		Embed:      parseCapabilityBackend(file.EmbedBackend),
		TTS:        parseCapabilityBackend(file.TTSBackend),
		STT:        parseCapabilityBackend(file.STTBackend),
		Proxy:      parseCapabilityBackend(file.ProxyBackend),
		Timeouts:   timeouts,
		Policy:     file.Policy.merge(),
		Backends:   make(map[string]BackendDef, len(file.Backends)),
		Routers:    routers,
		Switchyard: mergeSwitchyardFile(file.Switchyard),
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
		baseURL := ExpandEnv(strings.TrimSpace(entry.BaseURL))
		cfg.Backends[id] = BackendDef{
			ID:           id,
			Remote:       entry.Remote,
			Extension:    strings.TrimSpace(entry.Extension),
			BaseURL:      strings.TrimRight(baseURL, "/"),
			Auth:         entry.Auth,
			Capabilities: caps,
			Models:       entry.Models,
		}
	}
	return cfg, true, nil
}

func defaultRouterFromEnv(opts ServeOptions) RouterConfig {
	backend := strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
	return RouterConfig{
		TTS:      CapabilityBackend{Enabled: true, Default: "mlx_local"},
		STT:      CapabilityBackend{Enabled: true, Default: "mlx_local"},
		Proxy:    CapabilityBackend{Enabled: true, Default: "mlx_local"},
		Policy:   DefaultRouterPolicy(),
		Timeouts: DefaultTimeouts(),
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
		cfg.Embed.Default = v
		cfg.Embed.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_CHAT_BACKEND")); v != "" {
		cfg.Chat.Default = v
		cfg.Chat.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_VISION_BACKEND")); v != "" {
		cfg.Vision.Default = v
		cfg.Vision.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_TTS_BACKEND")); v != "" {
		cfg.TTS.Default = v
		cfg.TTS.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_STT_BACKEND")); v != "" {
		cfg.STT.Default = v
		cfg.STT.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_PROXY_BACKEND")); v != "" {
		cfg.Proxy.Default = v
		cfg.Proxy.Enabled = true
	}
	// CLI --backend overrides mlx_local URL when present.
	if opts.BackendURL != "" {
		if b, ok := cfg.Backends["mlx_local"]; ok {
			b.BaseURL = strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
			cfg.Backends["mlx_local"] = b
		}
	}
}

func applySwitchyardEnvOverrides(cfg *RouterConfig) {
	if v := strings.TrimSpace(os.Getenv("POLYPUS_SWITCHYARD_BASE_URL")); v != "" {
		cfg.Switchyard.BaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_SWITCHYARD_CONFIG")); v != "" {
		cfg.Switchyard.ConfigPath = v
	}
}

// SwitchyardEnabled reports whether the stack expects a Switchyard process (POLYPUS_SWITCHYARD, default on).
func SwitchyardEnabled() bool {
	v := strings.TrimSpace(os.Getenv("POLYPUS_SWITCHYARD"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func normalizeCapabilityBackend(cfg *RouterConfig, cap *CapabilityBackend, field string, allowMLXFill bool) error {
	if !cap.Enabled {
		cap.Default = ""
		return nil
	}
	if cap.Default == "" {
		if allowMLXFill {
			if _, ok := cfg.Backends["mlx_local"]; ok {
				cap.Default = "mlx_local"
				return nil
			}
		}
		return fmt.Errorf("router: %s.default required (configured backend unavailable; set %s.enabled: false or pick a backend that exists)", field, field)
	}
	return nil
}

func normalizeRouterConfig(cfg *RouterConfig) error {
	if len(cfg.Backends) == 0 {
		return fmt.Errorf("router: no backends configured")
	}
	if err := normalizeCapabilityBackend(cfg, &cfg.Chat, "chat_backend", false); err != nil {
		return err
	}
	if err := normalizeCapabilityBackend(cfg, &cfg.Vision, "vision_backend", false); err != nil {
		return err
	}
	if err := normalizeCapabilityBackend(cfg, &cfg.Embed, "embed_backend", false); err != nil {
		return err
	}
	if err := normalizeCapabilityBackend(cfg, &cfg.TTS, "tts_backend", true); err != nil {
		return err
	}
	if cfg.STT.Enabled && cfg.STT.Default == "" && cfg.TTS.Enabled && cfg.TTS.Default != "" {
		cfg.STT.Default = cfg.TTS.Default
	}
	if err := normalizeCapabilityBackend(cfg, &cfg.STT, "stt_backend", true); err != nil {
		return err
	}
	if cfg.Proxy.Enabled && cfg.Proxy.Default == "" && cfg.TTS.Enabled && cfg.TTS.Default != "" {
		cfg.Proxy.Default = cfg.TTS.Default
	}
	if err := normalizeCapabilityBackend(cfg, &cfg.Proxy, "proxy_backend", true); err != nil {
		return err
	}
	for id, b := range cfg.Backends {
		if b.ID == "" {
			b.ID = id
		}
		if b.BaseURL == "" {
			return fmt.Errorf("router: backends.%s.base_url required", id)
		}
		if b.Remote {
			if _, err := b.Auth.ResolveBearerToken(); err != nil {
				return fmt.Errorf("router: backends.%s: %w", id, err)
			}
		}
		if len(b.Capabilities) == 0 {
			return fmt.Errorf("router: backends.%s.capabilities required", id)
		}
		if err := b.Models.validate(id); err != nil {
			return err
		}
		cfg.Backends[id] = b
	}
	if cfg.Chat.Enabled {
		if err := requireBackend(cfg, cfg.Chat.Default, CapChat, "chat_backend.default"); err != nil {
			return err
		}
	}
	if cfg.Vision.Enabled {
		if err := requireBackend(cfg, cfg.Vision.Default, CapVision, "vision_backend.default"); err != nil {
			return err
		}
	}
	if cfg.Embed.Enabled {
		if err := requireBackend(cfg, cfg.Embed.Default, CapEmbed, "embed_backend.default"); err != nil {
			return err
		}
	}
	if cfg.TTS.Enabled {
		if err := requireBackend(cfg, cfg.TTS.Default, CapTTS, "tts_backend.default"); err != nil {
			return err
		}
	}
	if cfg.STT.Enabled {
		if err := requireBackend(cfg, cfg.STT.Default, CapSTT, "stt_backend.default"); err != nil {
			return err
		}
	}
	if cfg.Proxy.Enabled {
		if err := requireBackend(cfg, cfg.Proxy.Default, CapVoices, "proxy_backend.default"); err != nil {
			return err
		}
	}
	return validateRouters(cfg)
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
	if id := c.EffectiveProxyBackend(); id != "" {
		if b, ok := c.Backends[id]; ok {
			return b.BaseURL
		}
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
