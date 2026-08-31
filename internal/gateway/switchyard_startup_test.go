package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestNewHandlerDoesNotWriteSwitchyardTOML(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "1")
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "routes.toml")
	content := `
default_chat_backend: leaf
default_tts_backend: leaf
default_stt_backend: leaf
default_proxy_backend: leaf
switchyard:
  config_path: ` + tomlPath + `
backends:
  leaf:
    base_url: http://127.0.0.1:19999
    capabilities: [chat, tts, stt, voices]
    models:
      allow:
        - m
routers:
  r1:
    capability: chat
    route:
      type: passthrough
      target: leaf/m
`
	writeConfig(t, dir, content)

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:19999"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if g, ok := handler.(*Gateway); ok {
			g.Close()
		}
	})
	if _, err := os.Stat(tomlPath); !os.IsNotExist(err) {
		t.Fatalf("NewHandler must not write Switchyard TOML: err=%v", err)
	}
}

func TestEnsureSwitchyardConfigWritesTOML(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "1")
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "routes.toml")
	content := `
default_chat_backend: leaf
default_tts_backend: leaf
default_stt_backend: leaf
default_proxy_backend: leaf
switchyard:
  config_path: ` + tomlPath + `
backends:
  leaf:
    base_url: http://127.0.0.1:19999
    capabilities: [chat, tts, stt, voices]
    models:
      allow:
        - m
routers:
  r1:
    capability: chat
    route:
      type: passthrough
      target: leaf/m
`
	writeConfig(t, dir, content)

	opts := config.ServeOptions{BackendURL: "http://127.0.0.1:19999"}
	handler, err := NewHandler(opts)
	if err != nil {
		t.Fatal(err)
	}
	g := handler.(*Gateway)
	t.Cleanup(g.Close)

	if err := writeSwitchyardConfig(g, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tomlPath); err != nil {
		t.Fatalf("expected TOML at %s: %v", tomlPath, err)
	}
}
