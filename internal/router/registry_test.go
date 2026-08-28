package router

import (
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	cfg := config.RouterConfig{
		DefaultTTSBackend:   "mlx_local",
		DefaultSTTBackend:   "mlx_local",
		DefaultProxyBackend: "mlx_local",
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS, config.CapSTT, config.CapVoices},
			},
			"alt_stt": {
				ID:           "alt_stt",
				BaseURL:      "http://127.0.0.1:9000",
				Capabilities: []config.Capability{config.CapSTT},
			},
		},
	}
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestResolveTTSDefaultBackend(t *testing.T) {
	reg := testRegistry(t)
	p, m, err := reg.ResolveTTS("mlx-community/Qwen3-TTS")
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != "mlx_local" || m != "mlx-community/Qwen3-TTS" {
		t.Fatalf("got %s %q", p, m)
	}
}

func TestResolveTTSEmptyModelUsesDefaultBackend(t *testing.T) {
	reg := testRegistry(t)
	p, m, err := reg.ResolveTTS("")
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != "mlx_local" || m != "" {
		t.Fatalf("got %s %q", p, m)
	}
}

func TestResolveSTTEmptyModelUsesDefaultBackend(t *testing.T) {
	reg := testRegistry(t)
	p, m, err := reg.ResolveSTT("")
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != "mlx_local" || m != "" {
		t.Fatalf("got %s %q", p, m)
	}
}

func TestNewRegistryRejectsCloudflareWithoutAccountID(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")
	cfg := config.RouterConfig{
		DefaultTTSBackend:   "mlx_local",
		DefaultSTTBackend:   "mlx_local",
		DefaultProxyBackend: "mlx_local",
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS, config.CapSTT, config.CapVoices},
			},
			"cf_local": {
				ID:           "cf_local",
				Remote:       true,
				Extension:    config.ExtensionCloudflare,
				BaseURL:      "https://api.cloudflare.com/client/v4/accounts//ai/v1",
				Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	_, err := NewRegistry(cfg)
	if err == nil {
		t.Fatal("expected account id validation error")
	}
}

func TestResolveSTTPrefixBackend(t *testing.T) {
	reg := testRegistry(t)
	p, m, err := reg.ResolveSTT("alt_stt/whisper-large-v3")
	if err != nil {
		t.Fatal(err)
	}
	if string(p) != "alt_stt" || m != "whisper-large-v3" {
		t.Fatalf("got %s %q", p, m)
	}
}

func TestResolveSTTRejectsTTSOnlyBackend(t *testing.T) {
	cfg := config.RouterConfig{
		DefaultSTTBackend: "mlx_local",
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS},
			},
		},
	}
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = reg.ResolveSTT("whisper")
	if err == nil {
		t.Fatal("expected capability error")
	}
}

func TestResolveChatRequiresDefaultBackend(t *testing.T) {
	reg := testRegistry(t)
	_, _, err := reg.ResolveChat("glm")
	if err == nil {
		t.Fatal("expected error without chat backend")
	}
}

func TestResolveChatPrefixBackend(t *testing.T) {
	cfg := config.RouterConfig{
		DefaultChatBackend:  "cf_local",
		DefaultTTSBackend:   "mlx_local",
		DefaultSTTBackend:   "mlx_local",
		DefaultProxyBackend: "mlx_local",
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS, config.CapSTT, config.CapVoices},
			},
			"cf_local": {
				ID:           "cf_local",
				BaseURL:      "http://127.0.0.1:1323",
				Capabilities: []config.Capability{config.CapChat, config.CapVision},
			},
		},
	}
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id, model, err := reg.ResolveChat("cf_local/@cf/zai-org/glm-4.7-flash")
	if err != nil {
		t.Fatal(err)
	}
	if id != "cf_local" || model != "@cf/zai-org/glm-4.7-flash" {
		t.Fatalf("got %s %q", id, model)
	}
}

func TestResolveEmbedPrefixBackend(t *testing.T) {
	cfg := config.RouterConfig{
		DefaultEmbedBackend: "lm_studio",
		DefaultTTSBackend:   "mlx_local",
		DefaultSTTBackend:   "mlx_local",
		DefaultProxyBackend: "mlx_local",
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS, config.CapSTT, config.CapVoices},
			},
			"lm_studio": {
				ID:           "lm_studio",
				BaseURL:      "http://127.0.0.1:1234/v1",
				Capabilities: []config.Capability{config.CapChat, config.CapVision, config.CapEmbed},
			},
		},
	}
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	id, model, err := reg.ResolveEmbed("lm_studio/text-embedding-nomic-embed-text-v1.5")
	if err != nil {
		t.Fatal(err)
	}
	if id != "lm_studio" || model != "text-embedding-nomic-embed-text-v1.5" {
		t.Fatalf("got %s %q", id, model)
	}
}

func TestResolveChatRejectsMLXBackend(t *testing.T) {
	cfg := config.RouterConfig{
		DefaultChatBackend:  "mlx_local",
		DefaultTTSBackend:   "mlx_local",
		DefaultSTTBackend:   "mlx_local",
		DefaultProxyBackend: "mlx_local",
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS, config.CapSTT, config.CapVoices},
			},
		},
	}
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = reg.ResolveChat("glm")
	if err == nil {
		t.Fatal("expected capability error")
	}
}
