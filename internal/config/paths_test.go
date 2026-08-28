package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigPathUsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "polypus", "config.yaml")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultConfigPathUsesHomeDotConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".config", "polypus", "config.yaml")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolveConfigPathPrefersEnv(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "explicit.yaml")
	if err := os.WriteFile(explicit, []byte("backends: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("POLYPUS_CONFIG", explicit)
	t.Setenv("POLYPUS_ROOT", "")
	if got := ResolveConfigPath(); got != explicit {
		t.Fatalf("got %q want %q", got, explicit)
	}
}

func TestResolveConfigPathPrefersHomeOverRepo(t *testing.T) {
	home := t.TempDir()
	xdgPolypus := filepath.Join(home, ".config", "polypus")
	if err := os.MkdirAll(xdgPolypus, 0o755); err != nil {
		t.Fatal(err)
	}
	homeCfg := filepath.Join(xdgPolypus, "config.yaml")
	if err := os.WriteFile(homeCfg, []byte("backends: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	repoCfg := filepath.Join(repo, "config.yaml")
	if err := os.WriteFile(repoCfg, []byte("backends: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("POLYPUS_CONFIG", "")
	t.Setenv("POLYPUS_ROOT", repo)
	if got := ResolveConfigPath(); got != homeCfg {
		t.Fatalf("got %q want %q", got, homeCfg)
	}
}

func TestResolveConfigPathFallsBackToRepo(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	repoCfg := filepath.Join(repo, "config.yaml")
	if err := os.WriteFile(repoCfg, []byte("backends: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("POLYPUS_CONFIG", "")
	t.Setenv("POLYPUS_ROOT", repo)
	if got := ResolveConfigPath(); got != repoCfg {
		t.Fatalf("got %q want %q", got, repoCfg)
	}
}

func TestDefaultModelsInventoryCachePath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	got, err := DefaultModelsInventoryCachePath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "polypus", "models-inventory.json")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolveModelsInventoryCachePathPrefersEnv(t *testing.T) {
	explicit := filepath.Join(t.TempDir(), "inv.json")
	t.Setenv("POLYPUS_MODELS_CACHE", explicit)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if got := ResolveModelsInventoryCachePath(); got != explicit {
		t.Fatalf("got %q want %q", got, explicit)
	}
}

func TestDefaultProcessComposeSockPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	got, err := DefaultProcessComposeSockPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "polypus", "process-compose.sock")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultStateDirUsesHomeLocalState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	got, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "polypus")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
