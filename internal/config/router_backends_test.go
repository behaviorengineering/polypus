package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func writeRouterFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertNoBackendsRefused checks a config file without backends fails with
// CodeInvalid and a message that names the file.
func assertNoBackendsRefused(t *testing.T, path string) {
	t.Helper()
	_, err := LoadRouterConfig(ServeOptions{ConfigPath: path})
	if err == nil {
		t.Fatal("a config file with no backends must fail, not fall back to the env default")
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("code %s, want %s: %v", derrors.CodeOf(err), derrors.CodeInvalid, err)
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "has no backends") {
		t.Fatalf("error should name the file and say it has no backends: %v", err)
	}
}

func TestLoadRouterConfigFileWithoutBackendsFails(t *testing.T) {
	path := writeRouterFile(t, `
chat_backend:
  enabled: true
  default: cf_local
routers:
  fast:
    capability: chat
    route:
      type: passthrough
      target: cf_local/@cf/a
`)
	assertNoBackendsRefused(t, path)
}

func TestLoadRouterConfigEmptyBackendsFails(t *testing.T) {
	assertNoBackendsRefused(t, writeRouterFile(t, "backends: {}\n"))
}

func TestLoadRouterConfigNoFileUsesEnvDefault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")
	cfg, err := LoadRouterConfig(ServeOptions{ConfigPath: missing, BackendURL: "http://127.0.0.1:1322/"})
	if err != nil {
		t.Fatal(err)
	}
	b, ok := cfg.Backends[mlxLocalID]
	if len(cfg.Backends) != 1 || !ok {
		t.Fatalf("backends %v, want only %s", cfg.BackendIDs(), mlxLocalID)
	}
	if b.BaseURL != "http://127.0.0.1:1322" {
		t.Fatalf("base url %q", b.BaseURL)
	}
}

func TestSwitchyardEnvRejectsUnknownWord(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "disable")
	opts := ServeOptions{ConfigPath: filepath.Join(t.TempDir(), "absent.yaml"), BackendURL: "http://127.0.0.1:1322"}
	_, err := LoadRouterConfig(opts)
	if err == nil {
		t.Fatal("POLYPUS_SWITCHYARD=disable must fail config load")
	}
	if derrors.CodeOf(err) != derrors.CodeInvalid || !strings.Contains(err.Error(), "POLYPUS_SWITCHYARD") {
		t.Fatalf("want CodeInvalid naming POLYPUS_SWITCHYARD, got %s: %v", derrors.CodeOf(err), err)
	}
	for _, word := range []string{"1", "true", "yes", "on", "0", "false", "no", "off", ""} {
		t.Setenv("POLYPUS_SWITCHYARD", word)
		if _, err := LoadRouterConfig(opts); err != nil {
			t.Errorf("POLYPUS_SWITCHYARD=%q: %v", word, err)
		}
	}
}
