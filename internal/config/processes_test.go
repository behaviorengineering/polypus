package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProcessFlagsMLXTrue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("processes:\n  mlx: true\nbackends:\n  cf_local:\n    base_url: http://127.0.0.1:9\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	flags, err := LoadProcessFlagsFrom("")
	if err != nil {
		t.Fatal(err)
	}
	if !flags.MLXSet() || flags.MLX == nil || !*flags.MLX {
		t.Fatalf("want mlx=true set, got %+v", flags)
	}
}

func TestLoadProcessFlagsMLXFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("processes:\n  mlx: false\ntts_backend:\n  enabled: true\n  default: cf_local\nbackends:\n  cf_local:\n    base_url: http://127.0.0.1:9\n    capabilities: [tts]\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	flags, err := LoadProcessFlagsFrom("")
	if err != nil {
		t.Fatal(err)
	}
	if !flags.MLXSet() || flags.MLX == nil || *flags.MLX {
		t.Fatalf("want mlx=false set, got %+v", flags)
	}
}

func TestLoadProcessFlagsOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("tts_backend:\n  enabled: true\n  default: cf_local\nbackends:\n  cf_local:\n    base_url: http://127.0.0.1:9\n    capabilities: [tts]\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	flags, err := LoadProcessFlagsFrom("")
	if err != nil {
		t.Fatal(err)
	}
	if flags.MLXSet() {
		t.Fatalf("want mlx unset, got %+v", flags)
	}
}

// TestLoadProcessFlagsExplicitPathMustExist keeps --config strict and the error
// free of repeated "process flags" prefixes.
func TestLoadProcessFlagsExplicitPathMustExist(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	_, err := LoadProcessFlagsFrom(missing)
	if err == nil || !strings.Contains(err.Error(), "not found") || !strings.Contains(err.Error(), missing) {
		t.Fatalf("want a not-found error naming %s, got %v", missing, err)
	}
	if n := strings.Count(err.Error(), "process flags"); n != 1 {
		t.Fatalf("\"process flags\" appears %d times: %v", n, err)
	}
}

func TestLoadProcessFlagsNoConfig(t *testing.T) {
	t.Setenv("POLYPUS_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("POLYPUS_ROOT", t.TempDir())

	flags, err := LoadProcessFlagsFrom("")
	if err != nil {
		t.Fatal(err)
	}
	if flags.MLXSet() {
		t.Fatalf("want unset, got %+v", flags)
	}
}

func TestLoadRouterConfigAcceptsProcesses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`processes:
  mlx: false
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
    base_url: http://127.0.0.1:9
    capabilities: [tts, stt, voices]
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	cfg, err := LoadRouterConfig(ServeOptions{BackendURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.TTS.Enabled || cfg.TTS.Default != "cf_local" {
		t.Fatalf("tts: %+v", cfg.TTS)
	}
}

func TestParseBoolishEnv(t *testing.T) {
	if _, ok := parseBoolishEnv(""); ok {
		t.Fatal("empty should be unset")
	}
	if v, ok := parseBoolishEnv("1"); !ok || !v {
		t.Fatal("1")
	}
	if v, ok := parseBoolishEnv("off"); !ok || v {
		t.Fatal("off")
	}
}
