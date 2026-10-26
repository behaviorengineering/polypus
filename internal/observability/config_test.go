package observability

import (
	"os"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("POLYPUS_OTEL", "")
	t.Setenv("POLYPUS_OTEL_ENABLED", "")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("POLYPUS_FAILURE_DUMP_DIR", "")
	t.Setenv("POLYPUS_SERVICE_NAME", "")
	t.Setenv("POLYPUS_PHOENIX", "")
	t.Setenv("POLYPUS_OTEL_SKIP_PATHS", "")
	cfg := mustLoadConfig(t)
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
	if len(cfg.SkipPaths) != 3 || cfg.SkipPaths[0] != "/health" || cfg.SkipPaths[1] != "/health/backends" || cfg.SkipPaths[2] != "/health/upstreams" {
		t.Fatalf("skip=%v", cfg.SkipPaths)
	}
}

func TestLoadConfigDisabled(t *testing.T) {
	t.Setenv("POLYPUS_OTEL", "0")
	cfg := mustLoadConfig(t)
	if cfg.Enabled {
		t.Fatal("expected tracing disabled")
	}
}

func TestParseSkipPaths(t *testing.T) {
	got := parseSkipPaths("")
	if len(got) != 3 || got[0] != "/health" || got[1] != "/health/backends" || got[2] != "/health/upstreams" {
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
	cfg := mustLoadConfig(t)
	if cfg.DumpDir != os.TempDir() {
		t.Fatalf("dump=%s", cfg.DumpDir)
	}
}

func TestLoadConfigSkipPathsOverride(t *testing.T) {
	t.Setenv("POLYPUS_OTEL_SKIP_PATHS", "/health,/v1/models")
	cfg := mustLoadConfig(t)
	if len(cfg.SkipPaths) != 2 || cfg.SkipPaths[0] != "/health" || cfg.SkipPaths[1] != "/v1/models" {
		t.Fatalf("skip=%v", cfg.SkipPaths)
	}
}

func TestLoadConfigHonoursStandardOTLPEndpoint(t *testing.T) {
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.example:4317")
	if got := mustLoadConfig(t).OTLPEndpoint; got != "http://collector.example:4317" {
		t.Fatalf("OTEL_EXPORTER_OTLP_ENDPOINT must win over the local default, got %q", got)
	}
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "http://polypus.example:4317")
	if got := mustLoadConfig(t).OTLPEndpoint; got != "http://polypus.example:4317" {
		t.Fatalf("POLYPUS_OTLP_ENDPOINT must win, got %q", got)
	}
}

func mustLoadConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadConfigRejectsValuesItCannotRead(t *testing.T) {
	t.Setenv("POLYPUS_OTEL", "disabled")
	t.Setenv("POLYPUS_FAILURE_DUMP_MAX_FILES", "abc")
	_, err := LoadConfig()
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("want invalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "POLYPUS_OTEL") && derrors.Field(err, "env") == "" {
		t.Fatalf("the error must name the variable: %v", err)
	}
}
