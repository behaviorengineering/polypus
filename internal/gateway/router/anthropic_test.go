package router

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

// anthropicTestKeyEnv names the env var that holds the test key.
const anthropicTestKeyEnv = "ANTHROPIC_API_KEY"

func anthropicBackend(baseURL string) config.BackendDef {
	return config.BackendDef{
		ID:           "claude_team",
		Remote:       true,
		Extension:    config.ExtensionAnthropic,
		BaseURL:      baseURL,
		Auth:         config.BackendAuth{BearerEnv: anthropicTestKeyEnv},
		Capabilities: []config.Capability{config.CapChat},
	}
}

func TestValidateAnthropicBackendURL(t *testing.T) {
	policy := config.DefaultRouterPolicy()
	for _, base := range []string{"", "https://api.anthropic.com", "https://api.anthropic.com/v1"} {
		if err := ValidateBackend(anthropicBackend(base), policy); err != nil {
			t.Fatalf("base %q: %v", base, err)
		}
	}
	if err := ValidateBackend(anthropicBackend("https://example.com"), policy); err == nil {
		t.Fatal("expected a non-anthropic host to be rejected")
	}
}

func TestGenericRemoteStillBlocksAnthropicHost(t *testing.T) {
	b := anthropicBackend("https://api.anthropic.com")
	b.Extension = ""
	if err := ValidateBackend(b, config.DefaultRouterPolicy()); err == nil {
		t.Fatal("expected api.anthropic.com blocked for a generic remote backend")
	}
}

func TestNewAccountRegistersAnthropicChat(t *testing.T) {
	t.Setenv(anthropicTestKeyEnv, "secret-key-at-least-twenty-ch")
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	for base, want := range map[string]string{
		"":                             "",
		"https://api.anthropic.com/":   "https://api.anthropic.com",
		"https://api.anthropic.com/v1": "https://api.anthropic.com",
	} {
		cfg := config.RouterConfig{
			Timeouts: config.DefaultTimeouts(),
			Backends: map[string]config.BackendDef{"claude_team": anthropicBackend(base)},
		}
		pc, err := NewAccount(cfg).GetConfigForProvider("claude_team")
		if err != nil {
			t.Fatal(err)
		}
		if pc.CustomProviderConfig.BaseProviderType != schemas.Anthropic {
			t.Fatalf("base type: got %q want anthropic", pc.CustomProviderConfig.BaseProviderType)
		}
		if pc.NetworkConfig.BaseURL != want {
			t.Fatalf("base %q: got %q want %q", base, pc.NetworkConfig.BaseURL, want)
		}
		ar := pc.CustomProviderConfig.AllowedRequests
		if ar == nil || !ar.ChatCompletion || !ar.ChatCompletionStream {
			t.Fatalf("want chat + stream: %+v", ar)
		}
	}
}

func TestAnthropicUsesNonRawChatBody(t *testing.T) {
	t.Setenv(anthropicTestKeyEnv, "secret-key-at-least-twenty-ch")
	reg, err := NewRegistry(config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{"claude_team": anthropicBackend("")},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{reg: reg}
	if c.bifrostUseRawChatBody("claude_team") {
		t.Fatal("anthropic must take the non-raw path so Bifrost translates to /v1/messages")
	}
}

func TestWithAutoPromptCacheAddsTopLevelMarker(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	req, err := bifrostChatRequest("claude_team", "claude-sonnet-5-5", nil, body, false)
	if err != nil {
		t.Fatal(err)
	}
	withAutoPromptCache(anthropicBackend(""), body, req)
	if req.Params == nil || req.Params.CacheControl == nil || req.Params.CacheControl.Type != schemas.CacheControlTypeEphemeral {
		t.Fatalf("want top-level ephemeral cache_control: %+v", req.Params)
	}
	if req.Params.CacheControl.TTL != nil {
		t.Fatalf("want the default 5-minute lifetime, got ttl %q", *req.Params.CacheControl.TTL)
	}
}

func TestWithAutoPromptCacheKeepsClientMarkers(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	req, err := bifrostChatRequest("claude_team", "claude-sonnet-5-5", nil, body, false)
	if err != nil {
		t.Fatal(err)
	}
	withAutoPromptCache(anthropicBackend(""), body, req)
	if req.Params != nil && req.Params.CacheControl != nil {
		t.Fatalf("client markers present; top-level marker must not be added: %+v", req.Params.CacheControl)
	}
}

func TestWithAutoPromptCacheIgnoresQuotedText(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"explain \"cache_control\" please"}]}`)
	req, err := bifrostChatRequest("claude_team", "claude-sonnet-5-5", nil, body, false)
	if err != nil {
		t.Fatal(err)
	}
	withAutoPromptCache(anthropicBackend(""), body, req)
	if req.Params == nil || req.Params.CacheControl == nil {
		t.Fatal("text that mentions cache_control is not a marker; top-level marker expected")
	}
}

func TestWithAutoPromptCacheOff(t *testing.T) {
	off := false
	b := anthropicBackend("")
	b.PromptCache = &off
	body := []byte(`{"messages":[]}`)
	req, err := bifrostChatRequest("claude_team", "claude-sonnet-5-5", nil, body, false)
	if err != nil {
		t.Fatal(err)
	}
	withAutoPromptCache(b, body, req)
	if req.Params != nil {
		t.Fatalf("prompt_cache: false must add nothing: %+v", req.Params)
	}
}

func TestWithAutoPromptCacheSkipsOtherBackends(t *testing.T) {
	body := []byte(`{"messages":[]}`)
	req, err := bifrostChatRequest("gemini_paid", "gemini-2.5-flash", nil, body, false)
	if err != nil {
		t.Fatal(err)
	}
	withAutoPromptCache(config.BackendDef{Extension: config.ExtensionGemini}, body, req)
	if req.Params != nil {
		t.Fatalf("non-anthropic backends get no automatic caching: %+v", req.Params)
	}
}
