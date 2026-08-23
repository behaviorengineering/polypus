package observability

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("POLYPUS_OTEL", "")
	t.Setenv("POLYPUS_OTEL_ENABLED", "")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("POLYPUS_FAILURE_DUMP_DIR", "")
	t.Setenv("POLYPUS_SERVICE_NAME", "")
	t.Setenv("POLYPUS_PHOENIX", "")
	t.Setenv("POLYPUS_OTEL_SKIP_PATHS", "")
	cfg := LoadConfig()
	if !cfg.Enabled {
		t.Fatal("expected tracing enabled by default")
	}
	if cfg.ServiceName != defaultServiceName {
		t.Fatalf("service=%s", cfg.ServiceName)
	}
	if cfg.OTLPEndpoint != defaultOTLPEndpoint {
		t.Fatalf("otlp=%s", cfg.OTLPEndpoint)
	}
	if cfg.DumpDir != defaultDumpDir {
		t.Fatalf("dump=%s", cfg.DumpDir)
	}
	if len(cfg.SkipPaths) != 2 || cfg.SkipPaths[0] != "/health" || cfg.SkipPaths[1] != "/health/backends" {
		t.Fatalf("skip=%v", cfg.SkipPaths)
	}
}

func TestLoadConfigDisabled(t *testing.T) {
	t.Setenv("POLYPUS_OTEL", "0")
	cfg := LoadConfig()
	if cfg.Enabled {
		t.Fatal("expected tracing disabled")
	}
}

func TestParseSkipPaths(t *testing.T) {
	got := parseSkipPaths("")
	if len(got) != 1 || got[0] != "/health" {
		t.Fatalf("default skip=%v", got)
	}
	got = parseSkipPaths("health, /ready/")
	if len(got) != 2 || got[0] != "/health" || got[1] != "/ready" {
		t.Fatalf("list skip=%v", got)
	}
	got = parseSkipPaths("none")
	if got == nil || len(got) != 0 {
		t.Fatalf("none skip=%v", got)
	}
}

func TestPathIsSkipped(t *testing.T) {
	skip := []string{"/health"}
	if !pathIsSkipped("/health", skip) {
		t.Fatal("expected /health skipped")
	}
	if !pathIsSkipped("/health/", skip) {
		t.Fatal("expected /health/ skipped")
	}
	if pathIsSkipped("/v1/chat/completions", skip) {
		t.Fatal("did not expect chat skipped")
	}
}

func TestLoadConfigDumpDirOverride(t *testing.T) {
	t.Setenv("POLYPUS_FAILURE_DUMP_DIR", os.TempDir())
	cfg := LoadConfig()
	if cfg.DumpDir != os.TempDir() {
		t.Fatalf("dump=%s", cfg.DumpDir)
	}
}

func TestLoadConfigSkipPathsOverride(t *testing.T) {
	t.Setenv("POLYPUS_OTEL_SKIP_PATHS", "/health,/v1/models")
	cfg := LoadConfig()
	if len(cfg.SkipPaths) != 2 || cfg.SkipPaths[0] != "/health" || cfg.SkipPaths[1] != "/v1/models" {
		t.Fatalf("skip=%v", cfg.SkipPaths)
	}
}
