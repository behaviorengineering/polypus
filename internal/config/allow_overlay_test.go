package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyAllowOverlayMissingFile(t *testing.T) {
	rcfg := gatedRouterConfig()
	if err := ApplyAllowOverlay(&rcfg, filepath.Join(t.TempDir(), "missing.yaml")); err != nil {
		t.Fatal(err)
	}
	if rcfg.Backends["cf"].Models.Allow[0] != "@cf/a" {
		t.Fatalf("baseline unchanged: %v", rcfg.Backends["cf"].Models.Allow)
	}
}

func TestApplyAllowOverlayUnion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(path, []byte(`backends:
  cf:
    allow:
      - "@cf/b"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	rcfg := gatedRouterConfig()
	if err := ApplyAllowOverlay(&rcfg, path); err != nil {
		t.Fatal(err)
	}
	if !ModelInAllowList("cf", "@cf/b", rcfg.Backends["cf"].Models.Allow) {
		t.Fatalf("expected @cf/b in allow: %v", rcfg.Backends["cf"].Models.Allow)
	}
}

func TestApplyAllowOverlaySkipsUngated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(path, []byte(`backends:
  open:
    allow:
      - "anything"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	rcfg := RouterConfig{
		Backends: map[string]BackendDef{
			"open": {ID: "open", Models: nil},
		},
	}
	if err := ApplyAllowOverlay(&rcfg, path); err != nil {
		t.Fatal(err)
	}
	if rcfg.Backends["open"].Models != nil {
		t.Fatal("ungated backend must not gain models gate")
	}
}

func TestOverlayAddRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "overlay.yaml")
	if err := OverlayAdd(path, "cf", "@cf/new"); err != nil {
		t.Fatal(err)
	}
	file, err := LoadAllowOverlay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Backends["cf"].Allow) != 1 || file.Backends["cf"].Allow[0] != "@cf/new" {
		t.Fatalf("got %v", file.Backends["cf"].Allow)
	}
	if err := OverlayRemove(path, "cf", "@cf/new"); err != nil {
		t.Fatal(err)
	}
	file, err = LoadAllowOverlay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Backends) != 0 && len(file.Backends["cf"].Allow) != 0 {
		t.Fatalf("expected empty after remove: %v", file.Backends)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
}

func TestResolveModelsAllowOverlayPathEnv(t *testing.T) {
	t.Setenv("POLYPUS_MODELS_ALLOW_OVERLAY", "/tmp/custom-overlay.yaml")
	if got := ResolveModelsAllowOverlayPath(); got != "/tmp/custom-overlay.yaml" {
		t.Fatalf("got %q", got)
	}
}

func gatedRouterConfig() RouterConfig {
	return RouterConfig{
		Backends: map[string]BackendDef{
			"cf": {
				ID: "cf",
				Models: &BackendModels{
					AllowConfigured: true,
					Allow:           []string{"@cf/a"},
				},
			},
		},
	}
}
