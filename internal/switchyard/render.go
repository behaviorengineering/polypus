package switchyard

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

// Render builds Switchyard routes.toml from Polypus router config.
// Passthrough routers are omitted; composed stage_router and llm_classifier entries are emitted.
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
		switch router.Route.Type {
		case config.RouteStageRouter:
			if err := renderStageRouter(&b, name, router); err != nil {
				return nil, err
			}
		case config.RouteLLMClassifier:
			if err := renderLLMClassifier(&b, name, router); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("switchyard render: routers.%s: unsupported composed type %q", name, router.Route.Type)
		}
	}
	return []byte(b.String()), nil
}

func renderStageRouter(b *strings.Builder, name string, router config.NamedRouter) error {
	capableTarget := name + "_capable"
	efficientTarget := name + "_efficient"
	fmt.Fprintf(b, "[targets.%s]\nid = %q\nllm_client = \"polypus\"\n\n", capableTarget, router.Route.Capable)
	fmt.Fprintf(b, "[targets.%s]\nid = %q\nllm_client = \"polypus\"\n\n", efficientTarget, router.Route.Efficient)
	fmt.Fprintf(b, "[routes.%s]\n", name)
	fmt.Fprintf(b, "id = %q\n", config.RouterPublicID(name))
	fmt.Fprintf(b, "type = \"stage_router\"\n")
	fmt.Fprintf(b, "capable_target = %q\n", capableTarget)
	fmt.Fprintf(b, "efficient_target = %q\n", efficientTarget)
	fmt.Fprintf(b, "picker = %q\n", router.Route.Picker)
	fmt.Fprintf(b, "confidence_threshold = %g\n", router.Route.ConfidenceThreshold)
	if router.Route.RecentTurnWindow != nil {
		fmt.Fprintf(b, "recent_turn_window = %d\n", *router.Route.RecentTurnWindow)
	}
	b.WriteString("\n")
	return nil
}

func renderLLMClassifier(b *strings.Builder, name string, router config.NamedRouter) error {
	route := router.Route
	if route.Mode != config.ClassifierModeCustom {
		return fmt.Errorf("switchyard render: routers.%s: llm_classifier mode %q unsupported", name, route.Mode)
	}
	classifierTarget := config.ClassifierTargetName(name)
	fmt.Fprintf(b, "[targets.%s]\nid = %q\nllm_client = \"polypus\"\n\n", classifierTarget, route.Classifier)
	for _, t := range route.Targets {
		fmt.Fprintf(b, "[targets.%s]\nid = %q\nllm_client = \"polypus\"\n\n", t.Name, t.Model)
	}
	fmt.Fprintf(b, "[routes.%s]\n", name)
	fmt.Fprintf(b, "id = %q\n", config.RouterPublicID(name))
	fmt.Fprintf(b, "type = \"llm_classifier\"\n")
	fmt.Fprintf(b, "mode = %q\n", config.ClassifierModeCustom)
	fmt.Fprintf(b, "classifier_target = %q\n", classifierTarget)
	fmt.Fprintf(b, "targets = [%s]\n", joinQuoted(targetNames(route.Targets)))
	fmt.Fprintf(b, "default_target = %q\n", route.DefaultTarget)
	fmt.Fprintf(b, "prompt = %s\n", tomlMultiline(route.Prompt))
	fmt.Fprintf(b, "response_schema = %s\n", tomlMultiline(route.ResponseSchema))
	if route.SessionAffinity != nil {
		fmt.Fprintf(b, "session_affinity = %t\n", *route.SessionAffinity)
	}
	if route.MessageHashFallback != nil {
		fmt.Fprintf(b, "message_hash_fallback = %t\n", *route.MessageHashFallback)
	}
	if route.RecentTurnWindow != nil {
		fmt.Fprintf(b, "recent_turn_window = %d\n", *route.RecentTurnWindow)
	}
	if route.MaxOutputTokens != nil {
		fmt.Fprintf(b, "max_output_tokens = %d\n", *route.MaxOutputTokens)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "[routes.%s.policy]\n", name)
	fmt.Fprintf(b, "type = %q\n", route.PolicyType)
	fmt.Fprintf(b, "selector = %q\n\n", route.PolicySelector)
	return nil
}

func targetNames(targets []config.ClassifierTarget) []string {
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.Name)
	}
	return names
}

func joinQuoted(values []string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, strconv.Quote(v))
	}
	return strings.Join(parts, ", ")
}

// tomlMultiline encodes s as a TOML multiline string.
// Prefers a literal ”' block when safe; otherwise uses a basic """ block with escaping.
func tomlMultiline(s string) string {
	if canUseTOMLLiteral(s) {
		return "'''\n" + s + "\n'''"
	}
	var b strings.Builder
	b.WriteString("\"\"\"\n")
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteByte('\n')
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteString("\n\"\"\"")
	return b.String()
}

func canUseTOMLLiteral(s string) bool {
	if strings.Contains(s, "'''") {
		return false
	}
	// A trailing single quote before the closing ''' is ambiguous for some parsers.
	if strings.HasSuffix(s, "'") {
		return false
	}
	return true
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
