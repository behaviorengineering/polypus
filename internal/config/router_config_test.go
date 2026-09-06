package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRouterYAMLModelsAllow(t *testing.T) {
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
chat_backend:
  enabled: true
  default: cf_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  cf_local:
    base_url: http://127.0.0.1:1323
    capabilities: [chat, vision, tts, stt, voices]
    models:
      sync: true
      allow:
        - "@cf/zai-org/glm-4.7-flash"
  blocked:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    models:
      allow: []
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cf := cfg.Backends["cf_local"]
	if cf.Models == nil || !cf.Models.HasAllowGate() {
		t.Fatal("cf allow gate missing")
	}
	if !cf.IsModelAllowed("cf_local/@cf/zai-org/glm-4.7-flash") {
		t.Fatal("expected allowed")
	}
	if cf.IsModelAllowed("@cf/other") {
		t.Fatal("expected denied")
	}
	blocked := cfg.Backends["blocked"]
	if !blocked.Models.HasAllowGate() || blocked.IsModelAllowed("anything") {
		t.Fatal("empty allow should gate all")
	}
	mlx := cfg.Backends["mlx_local"]
	if mlx.Models != nil && mlx.Models.HasAllowGate() {
		t.Fatal("mlx should have no allow gate")
	}
	if !mlx.IsModelAllowed("anything") {
		t.Fatal("open backend")
	}
}

func TestDefaultRouterFromEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("POLYPUS_CONFIG", "")
	t.Setenv("POLYPUS_ROOT", "")
	opts := ServeOptions{BackendURL: "http://127.0.0.1:1322"}
	cfg, err := LoadRouterConfig(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TTS.Enabled || cfg.TTS.Default != "mlx_local" {
		t.Fatalf("tts: %+v", cfg.TTS)
	}
	b := cfg.Backends["mlx_local"]
	if b.BaseURL != "http://127.0.0.1:1322" {
		t.Fatalf("url: %q", b.BaseURL)
	}
	if cfg.Timeouts.Chat != 120*time.Second || cfg.Timeouts.Max != 900*time.Second {
		t.Fatalf("default timeouts: %+v", cfg.Timeouts)
	}
}

func TestLoadRouterYAML(t *testing.T) {
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: alt_stt
proxy_backend:
  enabled: true
  default: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
  alt_stt:
    base_url: http://127.0.0.1:9000
    capabilities: [stt]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.STT.Enabled || cfg.STT.Default != "alt_stt" {
		t.Fatalf("stt: %+v", cfg.STT)
	}
	if len(cfg.Backends) != 2 {
		t.Fatalf("backends: %d", len(cfg.Backends))
	}
	if cfg.Timeouts.Chat != 120*time.Second {
		t.Fatalf("yaml omitted timeouts should default chat: %s", cfg.Timeouts.Chat)
	}
}

func TestLoadRouterYAMLTimeouts(t *testing.T) {
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
timeouts:
  chat: 90s
  backends:
    cf_local:
      chat: 30s
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeouts.Chat != 90*time.Second {
		t.Fatalf("chat: %s", cfg.Timeouts.Chat)
	}
	if cfg.Timeouts.ResolveChat("", "cf_local", false, false) != 30*time.Second {
		t.Fatalf("cf chat: %s", cfg.Timeouts.ResolveChat("", "cf_local", false, false))
	}
}

func TestTTSBackendDisabledSkipsRequire(t *testing.T) {
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: false
stt_backend:
  enabled: false
chat_backend:
  enabled: true
  default: lm_studio
backends:
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat, vision, embed]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTS.Enabled || cfg.TTS.Default != "" {
		t.Fatalf("tts should be off: %+v", cfg.TTS)
	}
	if cfg.STT.Enabled || cfg.STT.Default != "" {
		t.Fatalf("stt should be off: %+v", cfg.STT)
	}
	if cfg.Proxy.Enabled || cfg.Proxy.Default != "" {
		t.Fatalf("proxy should stay empty when speech off: %+v", cfg.Proxy)
	}
}

func TestTTSBackendCFCloudOK(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "1")
	t.Setenv("CF_AI_API_KEY", "secret")
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
proxy_backend:
  enabled: true
  default: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/x/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [tts, stt, voices]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EffectiveTTSBackend() != "cf_local" {
		t.Fatalf("tts: %+v", cfg.TTS)
	}
}

func TestTTSBackendEnabledAfterStripFailsWithoutMLX(t *testing.T) {
	t.Setenv("INFERENCE_CLOUD_CASE", "0")
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
chat_backend:
  enabled: true
  default: lm_studio
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: https://api.cloudflare.com/client/v4/accounts/x/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [chat, tts, stt, voices]
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat, vision, embed]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil {
		t.Fatal("expected error when TTS enabled but CF stripped")
	}
	msg := err.Error()
	if !strings.Contains(msg, "tts_backend.default required") {
		t.Fatalf("want tts_backend.default required, got %v", err)
	}
	if strings.Contains(msg, "mlx_local not in backends") {
		t.Fatalf("must not invent mlx error: %v", err)
	}
}

func TestTTSBackendAutoFillMLXWhenPresent(t *testing.T) {
	dir := t.TempDir()
	content := `
tts_backend:
  enabled: true
stt_backend:
  enabled: true
proxy_backend:
  enabled: true
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	cfg, err := LoadRouterConfig(ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TTS.Default != "mlx_local" || cfg.STT.Default != "mlx_local" {
		t.Fatalf("tts=%+v stt=%+v", cfg.TTS, cfg.STT)
	}
	if !cfg.Proxy.Enabled || cfg.Proxy.Default != "mlx_local" {
		t.Fatalf("proxy: %+v", cfg.Proxy)
	}
}

