package switchyard

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

// Render builds Switchyard routes.toml from Polypus router config.
// Passthrough routers are omitted; only composed stage_router entries are emitted.
func Render(cfg config.RouterConfig, polypusBaseURL string) ([]byte, error) {
	polypusBaseURL = normalizePolypusBaseURL(polypusBaseURL)
	var b strings.Builder
	b.WriteString("schema_version = 1\n\n")
	fmt.Fprintf(&b, "[llm_clients.polypus]\nformat = \"openai_chat\"\nbase_url = %q\n\n", polypusBaseURL)

	names := make([]string, 0, len(cfg.Routers))
	for name, router := range cfg.Routers {
		if router.IsComposed() {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		router := cfg.Routers[name]
		capableTarget := name + "_capable"
		efficientTarget := name + "_efficient"
		fmt.Fprintf(&b, "[targets.%s]\nid = %q\nllm_client = \"polypus\"\n\n", capableTarget, router.Route.Capable)
		fmt.Fprintf(&b, "[targets.%s]\nid = %q\nllm_client = \"polypus\"\n\n", efficientTarget, router.Route.Efficient)
		fmt.Fprintf(&b, "[routes.%s]\n", name)
		fmt.Fprintf(&b, "id = %q\n", config.RouterPublicID(name))
		fmt.Fprintf(&b, "type = \"stage_router\"\n")
		fmt.Fprintf(&b, "capable_target = %q\n", capableTarget)
		fmt.Fprintf(&b, "efficient_target = %q\n", efficientTarget)
		fmt.Fprintf(&b, "picker = %q\n", router.Route.Picker)
		fmt.Fprintf(&b, "confidence_threshold = %g\n", router.Route.ConfidenceThreshold)
		if router.Route.RecentTurnWindow != nil {
			fmt.Fprintf(&b, "recent_turn_window = %d\n", *router.Route.RecentTurnWindow)
		}
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// WriteConfigIfNeeded renders and writes Switchyard TOML when routers are configured.
// Returns ("", nil) when there is nothing to emit (no routers).
func WriteConfigIfNeeded(cfg config.RouterConfig, polypusBaseURL string) (string, error) {
	if len(cfg.Routers) == 0 {
		return "", nil
	}
	return WriteConfig(cfg, polypusBaseURL)
}

// WriteConfig renders and writes Switchyard TOML to cfg.Switchyard.ConfigPath.
func WriteConfig(cfg config.RouterConfig, polypusBaseURL string) (string, error) {
	out, err := Render(cfg, polypusBaseURL)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(cfg.Switchyard.ConfigPath)
	if path == "" {
		var err error
		path, err = cfg.ResolveSwitchyardConfigPath()
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("switchyard config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", fmt.Errorf("switchyard config write: %w", err)
	}
	return path, nil
}

func normalizePolypusBaseURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = "http://127.0.0.1:1320"
	}
	if !strings.HasSuffix(raw, "/v1") {
		raw += "/v1"
	}
	return raw
}
