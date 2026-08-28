package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRouterKnownFieldsRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	content := `
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
routers:
  scribe:
    capability: chat
    route:
      type: passthrough
      target: mlx_local/foo
    unknown_key: bad
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices, chat]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil {
		t.Fatal("expected unknown router key error")
	}
	if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "unknown_key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRouterForbidsRouterBackend(t *testing.T) {
	dir := t.TempDir()
	content := `
default_tts_backend: mlx_local
default_stt_backend: mlx_local
default_proxy_backend: mlx_local
default_chat_backend: mlx_local
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices, chat]
  router:
    base_url: http://127.0.0.1:9999
    capabilities: [chat]
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("expected reserved backend error, got: %v", err)
	}
}

func TestLoadRouterStageRouterValidation(t *testing.T) {
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
    capabilities: [chat]
    models:
      allow:
        - "@cf/zai-org/glm-4.7-flash"
        - "@cf/google/gemma-4-26b-a4b-it"
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    models:
      allow:
        - qwen2.5-7b
routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/zai-org/glm-4.7-flash
      efficient: lm_studio/qwen2.5-7b
  scribe:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/google/gemma-4-26b-a4b-it
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
	if len(cfg.Routers) != 2 {
		t.Fatalf("routers: %d", len(cfg.Routers))
	}
	inv := cfg.Routers["investigator"]
	if !inv.IsComposed() || inv.Route.Picker != PickerEfficientFirst {
		t.Fatalf("investigator: %+v", inv)
	}
	if !cfg.HasComposedRouters() {
		t.Fatal("expected composed routers")
	}
	if cfg.Switchyard.BaseURL != "http://127.0.0.1:4000" {
		t.Fatalf("switchyard url: %q", cfg.Switchyard.BaseURL)
	}
}

func TestLoadRouterRejectsDisallowedLeaf(t *testing.T) {
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
    capabilities: [chat]
    models:
      allow:
        - "@cf/allowed"
routers:
  bad:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/denied
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected allow-list error, got: %v", err)
	}
}

func TestLoadRouterRejectsPassthroughStageFields(t *testing.T) {
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
    capabilities: [chat]
    models:
      allow:
        - "@cf/allowed"
routers:
  bad:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/allowed
      capable: cf_local/@cf/allowed
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "capable not allowed") {
		t.Fatalf("expected passthrough field error, got: %v", err)
	}
}

func TestLoadRouterRejectsStageRouterTarget(t *testing.T) {
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
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
        - "@cf/b"
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    models:
      allow:
        - qwen
routers:
  bad:
    capability: chat
    route:
      type: stage_router
      picker: efficient_first
      confidence_threshold: 0.5
      capable: cf_local/@cf/a
      efficient: lm_studio/qwen
      target: cf_local/@cf/a
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "target not allowed") {
		t.Fatalf("expected stage_router field error, got: %v", err)
	}
}

func TestGatewayBaseURLUsesLoopbackForWildcardBind(t *testing.T) {
	opts := ServeOptions{Host: "0.0.0.0", Port: 1320}
	if got := opts.GatewayBaseURL(); got != "http://127.0.0.1:1320" {
		t.Fatalf("gateway base url: %q", got)
	}
}

func TestSwitchyardEnabled(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "")
	if !SwitchyardEnabled() {
		t.Fatal("default should enable switchyard")
	}
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	if SwitchyardEnabled() {
		t.Fatal("0 should disable")
	}
	t.Setenv("POLYPUS_SWITCHYARD", "false")
	if SwitchyardEnabled() {
		t.Fatal("false should disable")
	}
}

func TestLoadRouterRejectsInvalidRouterName(t *testing.T) {
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
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
routers:
  bad.name:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/a
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("expected invalid name error, got: %v", err)
	}
}

func TestLoadRouterRequiresCapability(t *testing.T) {
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
    capabilities: [chat]
    models:
      allow:
        - "@cf/a"
routers:
  scribe:
    route:
      type: passthrough
      target: cf_local/@cf/a
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	_, err := LoadRouterConfig(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "capability required") {
		t.Fatalf("expected capability required, got: %v", err)
	}
}
