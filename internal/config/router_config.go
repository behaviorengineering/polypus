package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"sort"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

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

// mlxLocalID is the default local MLX audio backend's id.
const mlxLocalID = "mlx_local"

// Valid reports whether c is one of the known capabilities.
func (c Capability) Valid() bool {
	switch c {
	case CapChat, CapVision, CapEmbed, CapTTS, CapSTT, CapVoices, CapSystemOne, CapBatch:
		return true
	default:
		return false
	}
}

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
	// PromptCache turns automatic prompt caching off when false (Anthropic extension only; default on).
	PromptCache *bool `yaml:"prompt_cache"`
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

// IsAnthropicExtension reports whether the backend uses the native Anthropic Messages API extension.
func (b BackendDef) IsAnthropicExtension() bool {
	return b.HasExtension(ExtensionAnthropic)
}

// IsOpenRouter reports whether the backend is OpenRouter: the backend id openrouter (which
// Bifrost serves with its native OpenRouter provider) or a base_url on openrouter.ai.
func (b BackendDef) IsOpenRouter() bool {
	if strings.EqualFold(strings.TrimSpace(b.ID), "openrouter") {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(b.BaseURL))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "openrouter.ai" || strings.HasSuffix(host, ".openrouter.ai")
}

// AutoPromptCache reports whether Polypus adds a top-level cache_control to chat requests
// that carry none. On by default for Anthropic backends; prompt_cache: false turns it off.
func (b BackendDef) AutoPromptCache() bool {
	if !b.IsAnthropicExtension() {
		return false
	}
	return b.PromptCache == nil || *b.PromptCache
}

// baseURLOptional reports whether the extension supplies its own default upstream URL.
func (b BackendDef) baseURLOptional() bool {
	return b.IsGeminiExtension() || b.IsAnthropicExtension()
}

// CapabilityBackend is an optional capability default (chat, vision, embed, TTS, STT, proxy, systemone, batch).
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
	SystemOne  CapabilityBackend     `yaml:"-"`
	Batch      CapabilityBackend     `yaml:"-"`
	Timeouts   Timeouts              `yaml:"-"`
	Policy     RouterPolicy          `yaml:"policy"`
	Backends   map[string]BackendDef `yaml:"backends"`
	Routers    map[string]NamedRouter
	Switchyard SwitchyardConfig
	UIProxies  []UIProxy
	// DefaultTTSModel and DefaultSTTModel are the downstream models speech uses when
	// the request names none (POLYPUS_DEFAULT_MODEL, POLYPUS_DEFAULT_STT_MODEL),
	// read once at load instead of on every request.
	DefaultTTSModel string
	DefaultSTTModel string
	batchSpecified  bool
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
	Remote       bool              `yaml:"remote"`
	Extension    string            `yaml:"extension"`
	BaseURL      string            `yaml:"base_url"`
	Auth         BackendAuth       `yaml:"auth"`
	Capabilities []string          `yaml:"capabilities"`
	Models       *BackendModels    `yaml:"models"`
	PromptCache  *bool             `yaml:"prompt_cache"`
	ExtraHeaders map[string]string `yaml:"extra_headers"`
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
	out, err := loadRouterConfig(opts)
	if err != nil {
		return RouterConfig{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "config.LoadRouterConfig", "router config")
	}
	return out, nil
}

// loadRouterConfig is LoadRouterConfig before its error is typed at the package boundary.
func loadRouterConfig(opts ServeOptions) (RouterConfig, error) {
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
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
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
	if err := resolvePolypusSecrets(file.Secrets, opts.Keyring); err != nil {
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
	uiProxies, err := parseUIProxies(file.UIProxies)
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
		for _, raw := range entry.Capabilities {
			c := Capability(strings.TrimSpace(raw))
			if !c.Valid() {
				return RouterConfig{}, false, fmt.Errorf("router config %s: backends.%s.capabilities: unknown capability %q", path, id, raw)
			}
			caps = append(caps, c)
		}
		baseURL, err := expandEnv(strings.TrimSpace(entry.BaseURL))
		if err != nil {
			return RouterConfig{}, false, fmt.Errorf("router config %s: backends.%s.base_url: %w", path, id, err)
		}
		if baseURL != "" {
			if u, perr := url.Parse(baseURL); perr != nil || u.Scheme == "" || u.Host == "" {
				return RouterConfig{}, false, fmt.Errorf("router config %s: backends.%s.base_url %q needs a scheme and host", path, id, baseURL)
			}
		}
		headers, err := expandHeaders(entry.ExtraHeaders)
		if err != nil {
			return RouterConfig{}, false, fmt.Errorf("router config %s: backends.%s.extra_headers: %w", path, id, err)
		}
		cfg.Backends[id] = BackendDef{
			ID:           id,
			Remote:       entry.Remote,
			Extension:    strings.TrimSpace(entry.Extension),
			BaseURL:      strings.TrimRight(baseURL, "/"),
			Auth:         entry.Auth,
			Capabilities: caps,
			Models:       entry.Models,
			PromptCache:  entry.PromptCache,
			ExtraHeaders: headers,
		}
	}
	return cfg, true, nil
}

func defaultRouterFromEnv(opts ServeOptions) RouterConfig {
	backend := strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
	return RouterConfig{
		TTS:      CapabilityBackend{Enabled: true, Default: mlxLocalID},
		STT:      CapabilityBackend{Enabled: true, Default: mlxLocalID},
		Proxy:    CapabilityBackend{Enabled: true, Default: mlxLocalID},
		Policy:   DefaultRouterPolicy(),
		Timeouts: DefaultTimeouts(),
		Backends: map[string]BackendDef{
			mlxLocalID: {
				ID:           mlxLocalID,
				BaseURL:      backend,
				Capabilities: []Capability{CapTTS, CapSTT, CapVoices},
			},
		},
	}
}

func applyRouterEnvOverrides(cfg *RouterConfig, opts ServeOptions) {
	cfg.DefaultTTSModel = strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_MODEL"))
	cfg.DefaultSTTModel = strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_STT_MODEL"))
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
		if b, ok := cfg.Backends[mlxLocalID]; ok {
			b.BaseURL = strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
			cfg.Backends[mlxLocalID] = b
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
	return EnvBool("POLYPUS_SWITCHYARD", true)
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
			if _, ok := cfg.Backends[mlxLocalID]; ok {
				cap.Default = mlxLocalID
				return nil
			}
		}
		return fmt.Errorf("router: %s.default required (configured backend unavailable; set %s.enabled: false or pick a backend that exists)", field, field)
	}
	return nil
}

// capabilitySlot is one *_backend block: its YAML key, the capability its default
// backend must have, and how it is normalised.
type capabilitySlot struct {
	field string
	cap   Capability
	get   func(*RouterConfig) *CapabilityBackend
	// speech slots may stay without a default (normalizeCapabilityBackend's flag).
	speech bool
	// before fills a default from an earlier slot; it runs once those are normalised.
	before func(*RouterConfig)
}

// capabilitySlots lists the slots in normalisation order: stt and proxy borrow the
// tts default, and batch fills its own default, so they come after tts.
var capabilitySlots = []capabilitySlot{
	{field: "chat_backend", cap: CapChat, get: func(c *RouterConfig) *CapabilityBackend { return &c.Chat }},
	{field: "vision_backend", cap: CapVision, get: func(c *RouterConfig) *CapabilityBackend { return &c.Vision }},
	{field: "embed_backend", cap: CapEmbed, get: func(c *RouterConfig) *CapabilityBackend { return &c.Embed }},
	{field: "tts_backend", cap: CapTTS, speech: true, get: func(c *RouterConfig) *CapabilityBackend { return &c.TTS }},
	{field: "stt_backend", cap: CapSTT, speech: true, get: func(c *RouterConfig) *CapabilityBackend { return &c.STT }, before: func(c *RouterConfig) {
		inheritTTSDefault(c, &c.STT)
	}},
	{field: "proxy_backend", cap: CapVoices, speech: true, get: func(c *RouterConfig) *CapabilityBackend { return &c.Proxy }, before: func(c *RouterConfig) {
		inheritTTSDefault(c, &c.Proxy)
	}},
	{field: "systemone_backend", cap: CapSystemOne, get: func(c *RouterConfig) *CapabilityBackend { return &c.SystemOne }},
	{field: "batch_backend", cap: CapBatch, get: func(c *RouterConfig) *CapabilityBackend { return &c.Batch }, before: fillDefaultBatchBackend},
}

// inheritTTSDefault gives an enabled slot without a default the tts default.
func inheritTTSDefault(cfg *RouterConfig, slot *CapabilityBackend) {
	if slot.Enabled && slot.Default == "" && cfg.TTS.Enabled && cfg.TTS.Default != "" {
		slot.Default = cfg.TTS.Default
	}
}

func normalizeRouterConfig(cfg *RouterConfig) error {
	if len(cfg.Backends) == 0 {
		return fmt.Errorf("router: no backends configured")
	}
	for _, sl := range capabilitySlots {
		if sl.before != nil {
			sl.before(cfg)
		}
		if err := normalizeCapabilityBackend(cfg, sl.get(cfg), sl.field, sl.speech); err != nil {
			return err
		}
	}
	if err := normalizeBackends(cfg); err != nil {
		return err
	}
	for _, sl := range capabilitySlots {
		if slot := sl.get(cfg); slot.Enabled {
			if err := requireBackend(cfg, slot.Default, sl.cap, sl.field+".default"); err != nil {
				return err
			}
		}
	}
	return validateRouters(cfg)
}

// normalizeBackends fills each backend's id and checks its base URL, credential,
// capabilities and model gate.
func normalizeBackends(cfg *RouterConfig) error {
	for id, b := range cfg.Backends {
		if b.ID == "" {
			b.ID = id
		}
		if b.BaseURL == "" && !b.baseURLOptional() {
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
	if id := c.EffectiveProxyBackend(); id != "" {
		if b, ok := c.Backends[id]; ok {
			return b.BaseURL
		}
	}
	// Sorted ids, so the choice is the same on every call.
	ids := c.BackendIDs()
	for _, id := range ids {
		if b := c.Backends[id]; b.HasCapability(CapVoices) {
			return b.BaseURL
		}
	}
	if len(ids) > 0 {
		return c.Backends[ids[0]].BaseURL
	}
	return ""
}

// expandHeaders trims header names and expands ${VAR} in values. Nil stays nil.
func expandHeaders(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		name := strings.TrimSpace(k)
		if name == "" {
			return nil, fmt.Errorf("empty header name")
		}
		val, err := expandEnv(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = val
	}
	return out, nil
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
