package config

import (
	"os"
	"path/filepath"
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

	flags, err := LoadProcessFlags()
	if err != nil {
		t.Fatal(err)
	}
	if !flags.MLXSet() || flags.MLX == nil || !*flags.MLX {
		t.Fatalf("want mlx=true set, got %+v", flags)
	}
	if !flags.MLXEnabled(false) {
		t.Fatal("MLXEnabled(false) should be true when set")
	}
}

func TestLoadProcessFlagsMLXFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("processes:\n  mlx: false\ndefault_tts_backend: cf_local\nbackends:\n  cf_local:\n    base_url: http://127.0.0.1:9\n    capabilities: [tts]\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	flags, err := LoadProcessFlags()
	if err != nil {
		t.Fatal(err)
	}
	if !flags.MLXSet() || flags.MLX == nil || *flags.MLX {
		t.Fatalf("want mlx=false set, got %+v", flags)
	}
	if flags.MLXEnabled(true) {
		t.Fatal("MLXEnabled(true) should be false when set false")
	}
}

func TestLoadProcessFlagsOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("default_tts_backend: cf_local\nbackends:\n  cf_local:\n    base_url: http://127.0.0.1:9\n    capabilities: [tts]\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	flags, err := LoadProcessFlags()
	if err != nil {
		t.Fatal(err)
	}
	if flags.MLXSet() {
		t.Fatalf("want mlx unset, got %+v", flags)
	}
	if !flags.MLXEnabled(true) {
		t.Fatal("fallback true expected when unset")
	}
}

func TestLoadProcessFlagsNoConfig(t *testing.T) {
	t.Setenv("POLYPUS_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("POLYPUS_ROOT", t.TempDir())

	flags, err := LoadProcessFlags()
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
default_tts_backend: cf_local
default_stt_backend: cf_local
default_proxy_backend: cf_local
backends:
  cf_local:
    base_url: http://127.0.0.1:9
    capabilities: [tts, stt, voices]
`)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	t.Setenv("INFERENCE_CLOUD_CASE", "0")

	cfg, err := LoadRouterConfig(ServeOptions{BackendURL: "http://127.0.0.1:9"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultTTSBackend != "cf_local" {
		t.Fatalf("tts: %q", cfg.DefaultTTSBackend)
	}
}

func TestParseBoolishEnv(t *testing.T) {
	if _, ok := ParseBoolishEnv(""); ok {
		t.Fatal("empty should be unset")
	}
	if v, ok := ParseBoolishEnv("1"); !ok || !v {
		t.Fatal("1")
	}
	if v, ok := ParseBoolishEnv("off"); !ok || v {
		t.Fatal("off")
	}
}
