package observability

import (
	"os"
	"path"
	"strconv"
	"strings"
)

const (
	defaultServiceName  = "polypus"
	defaultOTLPEndpoint = "localhost:4317"
	defaultDumpDir      = "logs/inference-failures"
)

var defaultSkipPaths = []string{"/health"}

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
}

// LoadConfig reads POLYPUS_OTEL_* and POLYPUS_PHOENIX from the environment.
// Tracing is on by default so gateway spans join client traces in Phoenix.
func LoadConfig() Config {
	cfg := Config{
		Enabled:      envTruthy("POLYPUS_OTEL", true),
		ServiceName:  envOr("POLYPUS_SERVICE_NAME", defaultServiceName),
		OTLPEndpoint: strings.TrimSpace(os.Getenv("POLYPUS_OTLP_ENDPOINT")),
		DumpDir:      envOr("POLYPUS_FAILURE_DUMP_DIR", defaultDumpDir),
		DumpMaxFiles: envInt("POLYPUS_FAILURE_DUMP_MAX_FILES", 20),
		DumpMaxAgeH:  envInt("POLYPUS_FAILURE_DUMP_MAX_AGE_HOURS", 48),
		SkipPaths:    parseSkipPaths(os.Getenv("POLYPUS_OTEL_SKIP_PATHS")),
	}
	if cfg.OTLPEndpoint == "" && envTruthy("POLYPUS_PHOENIX", true) {
		cfg.OTLPEndpoint = defaultOTLPEndpoint
	}
	if v := strings.TrimSpace(os.Getenv("POLYPUS_OTEL_ENABLED")); v != "" {
		cfg.Enabled = envTruthy("POLYPUS_OTEL_ENABLED", cfg.Enabled)
	}
	return cfg
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envTruthy(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
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

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
