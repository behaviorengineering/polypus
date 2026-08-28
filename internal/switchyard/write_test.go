package switchyard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestWriteConfigCustomPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom", "routes.toml")
	cfg := config.RouterConfig{
		Switchyard: config.SwitchyardConfig{
			ConfigPath: custom,
		},
	}
	path, err := WriteConfig(cfg, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	if path != custom {
		t.Fatalf("path: %q", path)
	}
	raw, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `[llm_clients.polypus]`) {
		t.Fatalf("missing polypus client: %s", raw)
	}
}

func TestWriteConfigIfNeededSkipsEmpty(t *testing.T) {
	path, err := WriteConfigIfNeeded(config.RouterConfig{}, "http://127.0.0.1:1320")
	if err != nil {
		t.Fatal(err)
	}
	if path != "" {
		t.Fatalf("expected skip, got path %q", path)
	}
}
