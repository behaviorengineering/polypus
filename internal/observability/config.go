package observability

import (
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const (
	defaultServiceName  = "polypus"
	defaultOTLPEndpoint = "localhost:4317"
	defaultDumpDir      = "logs/inference-failures"
)

var defaultSkipPaths = []string{"/health", "/health/backends", "/health/upstreams"}

// Config controls Polypus OpenTelemetry export and local failure dumps.
type Config struct {
	Enabled      bool
	ServiceName  string
	OTLPEndpoint string
	DumpDir      string
	DumpMaxFiles int
	DumpMaxAgeH  int
	// SkipPaths are HTTP paths that must not create SERVER spans (probes).
	SkipPaths []string
	// Logger receives the dump writer's diagnostics. Init requires it; the process
	// entrypoint sets it.
	Logger *slog.Logger
}

// LoadConfig reads the tracing settings from the environment: POLYPUS_OTEL and
// POLYPUS_OTEL_ENABLED (on/off), POLYPUS_SERVICE_NAME, POLYPUS_OTLP_ENDPOINT (then
// OTEL_EXPORTER_OTLP_ENDPOINT, then the local Phoenix unless POLYPUS_PHOENIX is off),
// POLYPUS_FAILURE_DUMP_DIR, POLYPUS_FAILURE_DUMP_MAX_FILES,
// POLYPUS_FAILURE_DUMP_MAX_AGE_HOURS and POLYPUS_OTEL_SKIP_PATHS. Tracing is on by
// default so gateway spans join client traces in Phoenix. A value that does not
// parse is an error naming the variable, not a silent default.
func LoadConfig() (Config, error) {
	var errs []error
	boolEnv := func(key string, fallback bool) bool {
		v, err := config.ParseEnvBool(key, fallback)
		errs = append(errs, err)
		return v
	}
	intEnv := func(key string, fallback int) int {
		v, err := envInt(key, fallback)
		errs = append(errs, err)
		return v
	}
	cfg := Config{
		Enabled:      boolEnv("POLYPUS_OTEL", true),
		ServiceName:  envOr("POLYPUS_SERVICE_NAME", defaultServiceName),
		OTLPEndpoint: strings.TrimSpace(os.Getenv("POLYPUS_OTLP_ENDPOINT")),
		DumpDir:      envOr("POLYPUS_FAILURE_DUMP_DIR", defaultDumpDir),
		DumpMaxFiles: intEnv("POLYPUS_FAILURE_DUMP_MAX_FILES", 20),
		DumpMaxAgeH:  intEnv("POLYPUS_FAILURE_DUMP_MAX_AGE_HOURS", 48),
		SkipPaths:    parseSkipPaths(os.Getenv("POLYPUS_OTEL_SKIP_PATHS")),
	}
	// POLYPUS_OTLP_ENDPOINT, then the standard OTEL_EXPORTER_OTLP_ENDPOINT, then the
	// local Phoenix default.
	if cfg.OTLPEndpoint == "" {
		cfg.OTLPEndpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	if cfg.OTLPEndpoint == "" && boolEnv("POLYPUS_PHOENIX", true) {
		cfg.OTLPEndpoint = defaultOTLPEndpoint
	}
	cfg.Enabled = boolEnv("POLYPUS_OTEL_ENABLED", cfg.Enabled)
	// Dumps must be findable after the process exits: resolve the root against the
	// working directory now, so the logged path is the real one.
	if cfg.DumpDir != "" {
		abs, err := filepath.Abs(cfg.DumpDir)
		errs = append(errs, err)
		if err == nil {
			cfg.DumpDir = abs
		}
	}
	if err := errors.Join(errs...); err != nil {
		return cfg, derrors.Wrap(err, derrors.CodeInvalid, "observability.LoadConfig", "environment")
	}
	return cfg, nil
}

// ActiveDumpDir is where failure dumps go: DumpDir when tracing is on, else "" (no
// dumps).
func (c Config) ActiveDumpDir() string {
	if !c.Enabled {
		return ""
	}
	return c.DumpDir
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseSkipPaths(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return append([]string(nil), defaultSkipPaths...)
	}
	switch strings.ToLower(trimmed) {
	case "none", "off", "-":
		return []string{}
	}
	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		p := normalizeSkipPath(part)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func normalizeSkipPath(raw string) string {
	p := strings.TrimSpace(raw)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = path.Clean(p)
	if p == "." {
		return ""
	}
	return p
}

func pathIsSkipped(requestPath string, skip []string) bool {
	if len(skip) == 0 {
		return false
	}
	got := normalizeSkipPath(requestPath)
	for _, want := range skip {
		if got == want {
			return true
		}
	}
	return false
}

// envInt reads a non-negative integer env var: fallback when unset, an error when
// it does not parse.
func envInt(key string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback, derrors.New(derrors.CodeInvalid, "observability.envInt", "not a non-negative integer").
			With("env", key).With("value", raw)
	}
	return n, nil
}
