package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestGatewayConfigAcceptsSmokeBlock(t *testing.T) {
	content := `
chat_backend:
  enabled: true
  default: lm_studio
backends:
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    extension: typesafe
    capabilities: [chat, systemone]
    smoke:
      skip: true
      chat: [qwen2.5-7b]
      systemone: decider
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRouterConfig(ServeOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("a backend smoke block must load (strict decode): %v", err)
	}
	got := cfg.Backends["lm_studio"].Smoke
	if !got.Skip || !slices.Equal(got.Chat, []string{"qwen2.5-7b"}) || got.SystemOne != "decider" {
		t.Fatalf("smoke block %+v", got)
	}
}

func TestGatewayConfigRejectsUnknownSmokeKey(t *testing.T) {
	content := `
backends:
  lm_studio:
    base_url: http://127.0.0.1:1234/v1
    capabilities: [chat]
    smoke:
      skipp: true
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRouterConfig(ServeOptions{ConfigPath: path}); err == nil {
		t.Fatal("a typo inside smoke: must fail the strict decode")
	}
}
