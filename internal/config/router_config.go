package config

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"gopkg.in/yaml.v3"
)

// Capability is a speech backend feature set.
type Capability string

const (
	CapChat      Capability = "chat"
	CapVision    Capability = "vision"
	CapEmbed     Capability = "embed"
	CapTTS       Capability = "tts"
	CapSTT       Capability = "stt"
	CapVoices    Capability = "voices"
	CapSystemOne Capability = "systemone"
	CapBatch     Capability = "batch"
)

// BackendDef is one OpenAI-compatible inference worker.
type BackendDef struct {
	ID           string            `yaml:"-"`
	Remote       bool              `yaml:"remote"`
	Extension    string            `yaml:"extension"`
	BaseURL      string            `yaml:"base_url"`
	Auth         BackendAuth       `yaml:"auth"`
	ExtraHeaders map[string]string `yaml:"extra_headers"`
	Capabilities []Capability      `yaml:"capabilities"`
	Models       *BackendModels    `yaml:"models"`
}

// HasExtension reports whether the backend uses a named extension module.
func (b BackendDef) HasExtension(name string) bool {
	return strings.EqualFold(strings.TrimSpace(b.Extension), strings.TrimSpace(name))
}

// IsCloudflareExtension reports whether the backend uses the Cloudflare extension.
func (b BackendDef) IsCloudflareExtension() bool {
	return b.HasExtension(ExtensionCloudflare)
}

// IsGeminiExtension reports whether the backend uses the Google Gemini Developer API extension.
func (b BackendDef) IsGeminiExtension() bool {
	return b.HasExtension(ExtensionGemini)
}

// CapabilityBackend is an optional capability default (chat, vision, embed, TTS, STT, proxy, systemone, batch).
type CapabilityBackend struct {
	Enabled bool
	Default string
}

// RouterConfig holds multi-backend routing for the Polypus gateway.
type RouterConfig struct {
	Chat           CapabilityBackend     `yaml:"-"`
	Vision         CapabilityBackend     `yaml:"-"`
	Embed          CapabilityBackend     `yaml:"-"`
	TTS            CapabilityBackend     `yaml:"-"`
	STT            CapabilityBackend     `yaml:"-"`
	Proxy          CapabilityBackend     `yaml:"-"`
	SystemOne      CapabilityBackend     `yaml:"-"`
	Batch          CapabilityBackend     `yaml:"-"`
	Timeouts       Timeouts              `yaml:"-"`
	Policy         RouterPolicy          `yaml:"policy"`
	Backends       map[string]BackendDef `yaml:"backends"`
	Routers        map[string]NamedRouter
	Switchyard     SwitchyardConfig
	UIProxies      []UIProxy
	batchSpecified bool
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

// EffectiveSystemOneBackend returns the systemone default when enabled; otherwise empty.
func (c RouterConfig) EffectiveSystemOneBackend() string {
	if !c.SystemOne.Enabled {
		return ""
	}
	return c.SystemOne.Default
}

// EffectiveBatchBackend returns the batch default when batch is enabled; otherwise empty.
func (c RouterConfig) EffectiveBatchBackend() string {
	if !c.Batch.Enabled {
		return ""
	}
	return c.Batch.Default
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
	Secrets          []operatorconfig.Secret     `yaml:"secrets"`
	ChatBackend      capabilityBackendFile       `yaml:"chat_backend"`
	VisionBackend    capabilityBackendFile       `yaml:"vision_backend"`
	EmbedBackend     capabilityBackendFile       `yaml:"embed_backend"`
	TTSBackend       capabilityBackendFile       `yaml:"tts_backend"`
	STTBackend       capabilityBackendFile       `yaml:"stt_backend"`
	ProxyBackend     capabilityBackendFile       `yaml:"proxy_backend"`
	SystemOneBackend capabilityBackendFile       `yaml:"systemone_backend"`
	BatchBackend     capabilityBackendFile       `yaml:"batch_backend"`
	Timeouts         timeoutsFile                `yaml:"timeouts"`
	Policy           routerPolicyFile            `yaml:"policy"`
	Processes        processesFile               `yaml:"processes"`
	Backends         map[string]backendFileEntry `yaml:"backends"`
	Switchyard       switchyardFile              `yaml:"switchyard"`
	Routers          map[string]namedRouterFile  `yaml:"routers"`
	UIProxies        []uiProxyFile               `yaml:"ui_proxies"`
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
	path, err := ResolveConfigPathWithError()
	if err != nil {
		return RouterConfig{}, false, err
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
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return RouterConfig{}, false, fmt.Errorf("router config %s: %w", path, err)
	}
	if err := ResolvePolypusSecrets(file.Secrets, opts.Keyring); err != nil {
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
	uiProxies, err := ParseUIProxies(file.UIProxies)
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
		SystemOne:  parseCapabilityBackend(file.SystemOneBackend),
		Batch:      parseCapabilityBackend(file.BatchBackend),
		Timeouts:   timeouts,
		Policy:     file.Policy.merge(),
		Backends:   make(map[string]BackendDef, len(file.Backends)),
		Routers:    routers,
		Switchyard: mergeSwitchyardFile(file.Switchyard),
		UIProxies:  uiProxies,
	}
	if file.BatchBackend.Enabled != nil {
		cfg.batchSpecified = true
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
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_SYSTEMONE_BACKEND")); v != "" {
		cfg.SystemOne.Default = v
		cfg.SystemOne.Enabled = true
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_BATCH_BACKEND")); v != "" {
		cfg.Batch.Default = v
		cfg.Batch.Enabled = true
		cfg.batchSpecified = true
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

// fillDefaultBatchBackend turns Cloudflare batch on when YAML omitted batch_backend.
func fillDefaultBatchBackend(cfg *RouterConfig) {
	id := strings.TrimSpace(cfg.Batch.Default)
	if id == "" {
		id = firstCloudflareBackendID(cfg)
	}
	if id == "" {
		return
	}
	b, ok := cfg.Backends[id]
	if !ok || !b.IsCloudflareExtension() {
		return
	}
	if !cfg.batchSpecified {
		cfg.Batch.Enabled = true
		if strings.TrimSpace(cfg.Batch.Default) == "" {
			cfg.Batch.Default = id
		}
	}
	if cfg.Batch.Enabled {
		ensureCapability(cfg, id, CapBatch)
	}
}

func firstCloudflareBackendID(cfg *RouterConfig) string {
	if id := strings.TrimSpace(cfg.Chat.Default); id != "" {
		if b, ok := cfg.Backends[id]; ok && b.IsCloudflareExtension() {
			return id
		}
	}
	ids := make([]string, 0, len(cfg.Backends))
	for id, b := range cfg.Backends {
		if b.IsCloudflareExtension() {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[0]
}

func ensureCapability(cfg *RouterConfig, id string, cap Capability) {
	b, ok := cfg.Backends[id]
	if !ok || b.HasCapability(cap) {
		return
	}
	b.Capabilities = append(b.Capabilities, cap)
	cfg.Backends[id] = b
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
	if err := normalizeCapabilityBackend(cfg, &cfg.SystemOne, "systemone_backend", false); err != nil {
		return err
	}
	fillDefaultBatchBackend(cfg)
	if err := normalizeCapabilityBackend(cfg, &cfg.Batch, "batch_backend", false); err != nil {
		return err
	}
	for id, b := range cfg.Backends {
		if b.ID == "" {
			b.ID = id
		}
		if b.BaseURL == "" && !b.IsGeminiExtension() {
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
	if cfg.SystemOne.Enabled {
		if err := requireBackend(cfg, cfg.SystemOne.Default, CapSystemOne, "systemone_backend.default"); err != nil {
			return err
		}
	}
	if cfg.Batch.Enabled {
		if err := requireBackend(cfg, cfg.Batch.Default, CapBatch, "batch_backend.default"); err != nil {
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
