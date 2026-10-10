package router

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestNewAccountRegistersCloudflareChatAndSpeech(t *testing.T) {
	t.Setenv("CF_AI_API_KEY", "secret")
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:        "cf_local",
				Remote:    true,
				Extension: config.ExtensionCloudflare,
				BaseURL:   "https://api.cloudflare.com/client/v4/accounts/x/ai/v1",
				Auth:      config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{
					config.CapChat, config.CapEmbed, config.CapTTS, config.CapSTT,
				},
			},
		},
	}
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	acct := NewAccount(cfg)
	providers, err := acct.GetConfiguredProviders()
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0] != "cf_local" {
		t.Fatalf("providers=%v", providers)
	}
	pc, err := acct.GetConfigForProvider("cf_local")
	if err != nil {
		t.Fatal(err)
	}
	ar := pc.CustomProviderConfig.AllowedRequests
	if ar == nil || !ar.ChatCompletion || !ar.ChatCompletionStream || !ar.Embedding {
		t.Fatalf("want chat/stream/embed: %+v", ar)
	}
	if !ar.Speech || !ar.Transcription {
		t.Fatalf("CF must enable Bifrost speech/transcription: %+v", ar)
	}
	if ar.SpeechStream || ar.TranscriptionStream {
		t.Fatalf("CF must not enable speech streams: %+v", ar)
	}
	if !pc.SendBackRawResponse {
		t.Fatal("want SendBackRawResponse for upstream error diagnostics")
	}
	if pc.SendBackRawRequest {
		t.Fatal("SendBackRawRequest must stay off")
	}
}

func TestNewAccountRegistersSwitchyardWhenComposed(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "1")
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"leaf": {
				ID:           "leaf",
				BaseURL:      "http://127.0.0.1:1234",
				Capabilities: []config.Capability{config.CapChat},
			},
		},
		Routers: map[string]config.NamedRouter{
			"inv": {
				Name:       "inv",
				Capability: config.CapChat,
				Route: config.RouterRoute{
					Type:                config.RouteStageRouter,
					Capable:             "leaf/a",
					Efficient:           "leaf/b",
					Picker:              config.PickerEfficientFirst,
					ConfidenceThreshold: 0.5,
				},
			},
		},
		Switchyard: config.SwitchyardConfig{BaseURL: "http://127.0.0.1:4000"},
	}
	acct := NewAccount(cfg)
	providers, err := acct.GetConfiguredProviders()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range providers {
		if p == schemas.ModelProvider(ProviderSwitchyard) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing switchyard in %v", providers)
	}
	pc, err := acct.GetConfigForProvider(ProviderSwitchyard)
	if err != nil {
		t.Fatal(err)
	}
	if pc.NetworkConfig.BaseURL != "http://127.0.0.1:4000" {
		t.Fatalf("baseURL=%q", pc.NetworkConfig.BaseURL)
	}
	if !pc.CustomProviderConfig.AllowedRequests.ChatCompletion {
		t.Fatal("switchyard needs chat")
	}
	if !pc.SendBackRawResponse {
		t.Fatal("want SendBackRawResponse for upstream error diagnostics")
	}
	if pc.SendBackRawRequest {
		t.Fatal("SendBackRawRequest must stay off")
	}
}

func TestNewAccountRegistersGeminiChat(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "secret-key-at-least-twenty-ch")
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"gemini_studio": {
				ID:        "gemini_studio",
				Remote:    true,
				Extension: config.ExtensionGemini,
				Auth:      config.BackendAuth{BearerEnv: "GEMINI_API_KEY"},
				Capabilities: []config.Capability{
					config.CapChat,
				},
			},
		},
	}
	acct := NewAccount(cfg)
	pc, err := acct.GetConfigForProvider("gemini_studio")
	if err != nil {
		t.Fatal(err)
	}
	if pc.CustomProviderConfig.BaseProviderType != schemas.Gemini {
		t.Fatalf("base type: got %q want gemini", pc.CustomProviderConfig.BaseProviderType)
	}
	if pc.NetworkConfig.BaseURL != "" {
		t.Fatalf("empty yaml base_url should leave NetworkConfig.BaseURL empty for Bifrost default, got %q", pc.NetworkConfig.BaseURL)
	}
	ar := pc.CustomProviderConfig.AllowedRequests
	if ar == nil || !ar.ChatCompletion || !ar.ChatCompletionStream {
		t.Fatalf("want chat + stream: %+v", ar)
	}
	if ar.Speech || ar.Transcription {
		t.Fatalf("gemini chat backend should not enable speech: %+v", ar)
	}
}

func TestNewAccountCopiesExtraHeaders(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret-key-at-least-twenty-ch")
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"openrouter": {
				ID:      "openrouter",
				Remote:  true,
				BaseURL: "https://openrouter.ai/api/v1",
				Auth:    config.BackendAuth{BearerEnv: "OPENROUTER_API_KEY"},
				ExtraHeaders: map[string]string{
					"HTTP-Referer": "https://example.com",
					"X-Title":      "Polypus",
				},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	acct := NewAccount(cfg)
	pc, err := acct.GetConfigForProvider("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if pc.NetworkConfig.ExtraHeaders["HTTP-Referer"] != "https://example.com" {
		t.Fatalf("referer: %+v", pc.NetworkConfig.ExtraHeaders)
	}
	if pc.NetworkConfig.ExtraHeaders["X-Title"] != "Polypus" {
		t.Fatalf("title: %+v", pc.NetworkConfig.ExtraHeaders)
	}
}

func TestUsesBifrostSwitchyardAndLeaf(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	cfg := config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"leaf": {
				ID:           "leaf",
				BaseURL:      "http://127.0.0.1:9",
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{reg: reg}
	if !c.UsesBifrost("leaf") {
		t.Fatal("leaf should use Bifrost")
	}
	if c.UsesBifrost(ProviderSwitchyard) {
		t.Fatal("switchyard should be off when disabled and no composed routers")
	}

	cfg.Routers = map[string]config.NamedRouter{
		"inv": {
			Name:       "inv",
			Capability: config.CapChat,
			Route: config.RouterRoute{
				Type:                config.RouteStageRouter,
				Capable:             "leaf/a",
				Efficient:           "leaf/b",
				Picker:              config.PickerEfficientFirst,
				ConfidenceThreshold: 0.5,
			},
		},
	}
	cfg.Backends["leaf"] = config.BackendDef{
		ID:           "leaf",
		BaseURL:      "http://127.0.0.1:9",
		Capabilities: []config.Capability{config.CapChat},
		Models:       &config.BackendModels{Allow: []string{"a", "b"}},
	}
	reg2, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c2 := &Client{reg: reg2}
	if !c2.UsesBifrost(ProviderSwitchyard) {
		t.Fatal("composed routers register switchyard for Bifrost")
	}
}
