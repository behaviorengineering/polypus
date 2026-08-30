package router

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestNewAccountRegistersCloudflareChatNotSpeech(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
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
	if ar.Speech || ar.Transcription {
		t.Fatalf("CF must not enable Bifrost speech: %+v", ar)
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
