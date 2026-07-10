package config

import (
	"os"
	"path/filepath"
	"testing"
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
  lm_empty:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [embed]
    models:
      allow: []
`
	// need embed not as default - add default embed skip - cf can't be default embed
	// fix: remove lm_empty or give default embed
	content = `
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
}
