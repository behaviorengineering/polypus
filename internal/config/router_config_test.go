package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadRouterYAMLModelsAllow(t *testing.T) {
	dir := t.TempDir()
	content := `
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
default_chat_backend: cf_local
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
	if cfg.DefaultTTSBackend != "mlx_local" {
		t.Fatalf("tts default: %q", cfg.DefaultTTSBackend)
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
default_tts_backend: mlx_local
default_stt_backend: alt_stt
default_proxy_backend: mlx_local
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
	if cfg.DefaultSTTBackend != "alt_stt" {
		t.Fatalf("stt: %q", cfg.DefaultSTTBackend)
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
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
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
