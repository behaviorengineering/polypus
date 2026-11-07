package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/redact"

	"github.com/behaviorengineering/operatorconfig/pkg/operatorconfig"
	"gopkg.in/yaml.v3"
)

// Capability is a speech backend feature set.
type Capability string

// Capabilities a backend can declare in backends.<id>.capabilities.
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

// Backend extension names accepted in backends.<id>.extension; each selects a
// provider-specific client.
const (
	ExtensionCloudflare = "cloudflare"
	ExtensionGemini     = "gemini"
	ExtensionAnthropic  = "anthropic"
	ExtensionTypesafe   = "typesafe"
)

// openRouterBackendID is the backend id Bifrost serves with its native OpenRouter
// provider, which implements the SystemOne decision operation.
const openRouterBackendID = "openrouter"

// BackendDef is one OpenAI-compatible inference worker.
type BackendDef struct {
	ID           string
	Remote       bool
	Extension    string
	BaseURL      string
	Auth         BackendAuth
	ExtraHeaders map[string]string
	Capabilities []Capability
	Models       *BackendModels
	// PromptCache turns automatic prompt caching off when false (Anthropic extension only; default on).
	PromptCache *bool
	// Smoke holds the deploy smoke choices for this backend (read by polypus deploy).
	Smoke BackendSmoke
}

// hasExtension reports whether the backend uses a named extension module.
func (b BackendDef) hasExtension(name string) bool {
	return strings.EqualFold(strings.TrimSpace(b.Extension), strings.TrimSpace(name))
}

// IsCloudflareExtension reports whether the backend uses the Cloudflare extension.
func (b BackendDef) IsCloudflareExtension() bool {
	return b.hasExtension(ExtensionCloudflare)
}

// IsGeminiExtension reports whether the backend uses the Google Gemini Developer API extension.
func (b BackendDef) IsGeminiExtension() bool {
	return b.hasExtension(ExtensionGemini)
}

// IsAnthropicExtension reports whether the backend uses the native Anthropic Messages API extension.
func (b BackendDef) IsAnthropicExtension() bool {
	return b.hasExtension(ExtensionAnthropic)
}

// IsTypesafeExtension reports whether the backend is Typesafe's System One API, which
// Bifrost serves through its Typesafe provider (POST /v1/systemone).
func (b BackendDef) IsTypesafeExtension() bool {
	return b.hasExtension(ExtensionTypesafe)
}

// hasSystemOneRoute reports whether a SystemOne decision can reach the backend: the
// Cloudflare client (Workers AI jev), or Bifrost's decision operation on the native
// OpenRouter provider or the Typesafe provider.
func (b BackendDef) hasSystemOneRoute() bool {
	return b.IsCloudflareExtension() || b.ID == openRouterBackendID || b.IsTypesafeExtension()
}

// IsOpenRouter reports whether the backend is OpenRouter: the backend id openrouter (which
// Bifrost serves with its native OpenRouter provider) or a base_url on openrouter.ai.
func (b BackendDef) IsOpenRouter() bool {
	if strings.EqualFold(strings.TrimSpace(b.ID), openRouterBackendID) {
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
	return b.IsGeminiExtension() || b.IsAnthropicExtension() || b.IsTypesafeExtension()
}

// CapabilityBackend is an optional capability default (chat, vision, embed, TTS, STT, proxy, systemone, batch).
type CapabilityBackend struct {
	Enabled bool
	Default string
}

// RouterConfig holds multi-backend routing for the Polypus gateway.
type RouterConfig struct {
	Chat       CapabilityBackend
	Vision     CapabilityBackend
	Embed      CapabilityBackend
	TTS        CapabilityBackend
	STT        CapabilityBackend
	Proxy      CapabilityBackend
	SystemOne  CapabilityBackend
	Batch      CapabilityBackend
	Timeouts   Timeouts
	Policy     RouterPolicy
	Backends   map[string]BackendDef
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
func (b BackendDef) HasCapability(want Capability) bool {
	for _, c := range b.Capabilities {
		if c == want {
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
	Smoke        BackendSmoke      `yaml:"smoke"`
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
	if !fromFile {
		// No config file at all: the simple local default. A file with no backends
		// is refused in loadRouterFileAt, never swapped for this.
		cfg = defaultRouterFromEnv(opts)
	}
	if cfg.Timeouts.Max == 0 {
		cfg.Timeouts = DefaultTimeouts()
	}
	applyRouterEnvOverrides(&cfg, opts)
	applySwitchyardEnvOverrides(&cfg)
	// SwitchyardEnabled reads POLYPUS_SWITCHYARD while serving; an unknown word is
	// refused here, so a typo cannot flip Switchyard on behind the operator.
	if _, err := ParseEnvBool("POLYPUS_SWITCHYARD", true); err != nil {
		return RouterConfig{}, err
	}
	if err := normalizeRouterConfig(&cfg); err != nil {
		return RouterConfig{}, err
	}
	return cfg, nil
}

// loadRouterFile reads the router config the operator picked. An explicit
// ConfigPath must exist; only discovery may find no file and fall back to the env
// default.
func loadRouterFile(opts ServeOptions) (RouterConfig, bool, error) {
	if p := strings.TrimSpace(opts.ConfigPath); p != "" {
		cfg, found, err := loadRouterFileAt(opts, p)
		if err == nil && !found {
			return RouterConfig{}, false, derrors.New(derrors.CodeInvalid, "config.loadRouterFile",
				fmt.Sprintf("config file %s not found", p)).With("path", p)
		}
		return cfg, found, err
	}
	path, err := resolveConfigPath()
	if err != nil {
		return RouterConfig{}, false, err
	}
	if path == "" {
		return RouterConfig{}, false, nil
	}
	return loadRouterFileAt(opts, path)
}

// loadRouterFileAt reads the router config at path; a missing file means no file.
func loadRouterFileAt(opts ServeOptions, path string) (RouterConfig, bool, error) {
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
		return RouterConfig{}, false, routerDecodeError(path, err)
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
	// Sorted, so a file with several broken backends always names the same one.
	for _, rawID := range slices.Sorted(maps.Keys(file.Backends)) {
		entry := file.Backends[rawID]
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		def, err := parseBackendEntry(path, id, entry)
		if err != nil {
			return RouterConfig{}, false, err
		}
		cfg.Backends[id] = def
	}
	if len(cfg.Backends) == 0 {
		// A config the operator wrote is served or refused, never swapped for the
		// env default, which would drop its routers, policy and timeouts.
		// The path stays in the message too: the operator reads the message, and
		// derrors does not print fields.
		return RouterConfig{}, false, derrors.New(derrors.CodeInvalid, "config.loadRouterFileAt",
			fmt.Sprintf("config file %s has no backends", path)).With("path", path)
	}
	return cfg, true, nil
}

// parseBackendEntry checks one backends.<id> entry and expands its placeholders.
func parseBackendEntry(path, id string, entry backendFileEntry) (BackendDef, error) {
	caps := make([]Capability, 0, len(entry.Capabilities))
	for _, raw := range entry.Capabilities {
		c := Capability(strings.TrimSpace(raw))
		if !c.Valid() {
			return BackendDef{}, fmt.Errorf("router config %s: backends.%s.capabilities: unknown capability %q", path, id, raw)
		}
		caps = append(caps, c)
	}
	baseURL, err := expandEnv(strings.TrimSpace(entry.BaseURL))
	if err != nil {
		return BackendDef{}, fmt.Errorf("router config %s: backends.%s.base_url: %w", path, id, err)
	}
	if baseURL != "" {
		// The expanded value can hold credentials, so these errors never print it.
		u, perr := url.Parse(baseURL)
		if perr != nil {
			return BackendDef{}, fmt.Errorf("router config %s: backends.%s.base_url does not parse as a URL", path, id)
		}
		if u.Scheme == "" || u.Host == "" {
			return BackendDef{}, fmt.Errorf("router config %s: backends.%s.base_url needs a scheme and host (got scheme %q, host %q)", path, id, u.Scheme, u.Hostname())
		}
	}
	headers, err := expandHeaders(entry.ExtraHeaders)
	if err != nil {
		return BackendDef{}, fmt.Errorf("router config %s: backends.%s.extra_headers: %w", path, id, err)
	}
	return BackendDef{
		ID:           id,
		Remote:       entry.Remote,
		Extension:    strings.TrimSpace(entry.Extension),
		BaseURL:      strings.TrimRight(baseURL, "/"),
		Auth:         entry.Auth,
		Capabilities: caps,
		Models:       entry.Models,
		PromptCache:  entry.PromptCache,
		ExtraHeaders: headers,
		Smoke:        entry.Smoke,
	}, nil
}

// routerDecodeError names the file in a YAML decode failure and turns the bare EOF
// of an empty file into a message that says how to recover. Only the user config
// is rewritten by polypus init --force, so only it gets that hint.
func routerDecodeError(path string, err error) error {
	if errors.Is(err, io.EOF) {
		hint := "fill it in, or point POLYPUS_CONFIG at another file"
		if user, uerr := UserConfigFilePath(); uerr == nil && sameFile(user, path) {
			hint = "run polypus init --force"
		}
		return fmt.Errorf("router config %s: file is empty or holds only comments (%s)", path, hint)
	}
	return fmt.Errorf("router config %s: %w", path, err)
}

// sameFile reports whether a and b name the same path once cleaned and made
// absolute.
func sameFile(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}

// defaultRouterFromEnv is the config served when no file is found: mlx_local for
// speech, at the operator's backend URL or the default MLX port.
func defaultRouterFromEnv(opts ServeOptions) RouterConfig {
	backend := strings.TrimRight(strings.TrimSpace(opts.BackendURL), "/")
	if backend == "" {
		backend = httpBaseURL(DefaultHost, DefaultMLXPort)
	}
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
	for _, sl := range capabilitySlots {
		if v := strings.TrimSpace(os.Getenv(sl.env)); v != "" {
			slot := sl.get(cfg)
			slot.Default, slot.Enabled = v, true
		}
	}
	if strings.TrimSpace(os.Getenv("POLYPUS_DEFAULT_BATCH_BACKEND")) != "" {
		cfg.batchSpecified = true
	}
	// BackendURL is set only when the operator chose one (--backend, or
	// POLYPUS_BACKEND_URL, POLYPUS_MLX_URL, POLYPUS_MLX_HOST or POLYPUS_MLX_PORT), so
	// a config's own mlx_local base_url stands otherwise.
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

// SwitchyardEnabled reports whether the stack expects a Switchyard process
// (POLYPUS_SWITCHYARD, default on). LoadRouterConfig refuses a word ParseEnvBool
// does not know, so a gateway that loaded its config never reaches the error
// branch; there it reads as the default.
func SwitchyardEnabled() bool {
	on, err := ParseEnvBool("POLYPUS_SWITCHYARD", true)
	if err != nil {
		return true
	}
	return on
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

func ensureCapability(cfg *RouterConfig, id string, want Capability) {
	b, ok := cfg.Backends[id]
	if !ok || b.HasCapability(want) {
		return
	}
	b.Capabilities = append(b.Capabilities, want)
	cfg.Backends[id] = b
}

func normalizeCapabilityBackend(cfg *RouterConfig, slot *CapabilityBackend, field string, allowMLXFill bool) error {
	if !slot.Enabled {
		slot.Default = ""
		return nil
	}
	if slot.Default == "" {
		if allowMLXFill {
			if _, ok := cfg.Backends[mlxLocalID]; ok {
				slot.Default = mlxLocalID
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
	// env names the POLYPUS_DEFAULT_*_BACKEND variable that sets the slot's default
	// and turns it on.
	env        string
	capability Capability
	get        func(*RouterConfig) *CapabilityBackend
	// speech slots may stay without a default (normalizeCapabilityBackend's flag).
	speech bool
	// before fills a default from an earlier slot; it runs once those are normalised.
	before func(*RouterConfig)
}

// capabilitySlots lists the slots in normalisation order: stt and proxy borrow the
// tts default, and batch fills its own default, so they come after tts.
var capabilitySlots = []capabilitySlot{
	{field: "chat_backend", env: "POLYPUS_DEFAULT_CHAT_BACKEND", capability: CapChat, get: func(c *RouterConfig) *CapabilityBackend { return &c.Chat }},
	{field: "vision_backend", env: "POLYPUS_DEFAULT_VISION_BACKEND", capability: CapVision, get: func(c *RouterConfig) *CapabilityBackend { return &c.Vision }},
	{field: "embed_backend", env: "POLYPUS_DEFAULT_EMBED_BACKEND", capability: CapEmbed, get: func(c *RouterConfig) *CapabilityBackend { return &c.Embed }},
	{field: "tts_backend", env: "POLYPUS_DEFAULT_TTS_BACKEND", capability: CapTTS, speech: true, get: func(c *RouterConfig) *CapabilityBackend { return &c.TTS }},
	{field: "stt_backend", env: "POLYPUS_DEFAULT_STT_BACKEND", capability: CapSTT, speech: true, get: func(c *RouterConfig) *CapabilityBackend { return &c.STT }, before: func(c *RouterConfig) {
		inheritTTSDefault(c, &c.STT)
	}},
	{field: "proxy_backend", env: "POLYPUS_DEFAULT_PROXY_BACKEND", capability: CapVoices, speech: true, get: func(c *RouterConfig) *CapabilityBackend { return &c.Proxy }, before: func(c *RouterConfig) {
		inheritTTSDefault(c, &c.Proxy)
	}},
	{field: "systemone_backend", env: "POLYPUS_DEFAULT_SYSTEMONE_BACKEND", capability: CapSystemOne, get: func(c *RouterConfig) *CapabilityBackend { return &c.SystemOne }},
	{field: "batch_backend", env: "POLYPUS_DEFAULT_BATCH_BACKEND", capability: CapBatch, get: func(c *RouterConfig) *CapabilityBackend { return &c.Batch }, before: fillDefaultBatchBackend},
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
	fillCloudflareChatTimeouts(cfg)
	for _, sl := range capabilitySlots {
		if slot := sl.get(cfg); slot.Enabled {
			if err := requireBackend(cfg, slot.Default, sl.capability, sl.field+".default"); err != nil {
				return err
			}
		}
	}
	if err := validateRouters(cfg); err != nil {
		return err
	}
	return fillSwitchyardDefaults(cfg)
}

// normalizeBackends fills each backend's id and checks its base URL, credential,
// capabilities and model gate.
func normalizeBackends(cfg *RouterConfig) error {
	// Sorted, so a config with several broken backends always names the same one.
	for _, id := range slices.Sorted(maps.Keys(cfg.Backends)) {
		b := cfg.Backends[id]
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
		if b.HasCapability(CapSystemOne) && !b.hasSystemOneRoute() {
			return derrors.New(derrors.CodeInvalid, "config.normalizeBackends",
				"backends."+id+": capability systemone needs extension cloudflare or typesafe, or the backend id openrouter").
				With("backend", id)
		}
		if err := b.Models.validate(id); err != nil {
			return err
		}
		cfg.Backends[id] = b
	}
	return nil
}

func requireBackend(cfg *RouterConfig, id string, want Capability, field string) error {
	b, ok := cfg.Backends[id]
	if !ok {
		return fmt.Errorf("router: %s %q not found in backends", field, id)
	}
	if !b.HasCapability(want) {
		return fmt.Errorf("router: %s %q lacks capability %s", field, id, want)
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
	slices.Sort(ids)
	return ids
}

// LogStartup logs each capability slot's default backend, then the backends and
// the router count, so the startup log describes the config being served.
func (c RouterConfig) LogStartup(logger *slog.Logger) {
	if logger == nil {
		return
	}
	for _, sl := range capabilitySlots {
		slot := sl.get(&c)
		logger.Info("capability", "capability", sl.field, "enabled", slot.Enabled, "default", slot.Default)
	}
	for _, id := range c.BackendIDs() {
		b := c.Backends[id]
		logger.Info("backend", "id", id, "base_url", redact.URL(b.BaseURL), "capabilities", b.Capabilities)
	}
	if len(c.Routers) > 0 {
		logger.Info("routers", "count", len(c.Routers))
	}
}
