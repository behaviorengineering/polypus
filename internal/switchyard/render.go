package switchyard

import (
	"fmt"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/statefile"
	"github.com/pelletier/go-toml/v2"
)

// Render builds Switchyard routes.toml from Polypus router config.
// Passthrough routers are omitted; composed stage_router, llm_classifier and fallback entries are emitted.
func Render(cfg config.RouterConfig, polypusBaseURL string) ([]byte, error) {
	out, err := render(cfg, polypusBaseURL)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), "switchyard.Render", "render routes")
	}
	return out, nil
}

// routesFile is Switchyard's routes.toml.
type routesFile struct {
	SchemaVersion int                  `toml:"schema_version"`
	LLMClients    map[string]llmClient `toml:"llm_clients"`
	Targets       map[string]target    `toml:"targets,omitempty"`
	Routes        map[string]route     `toml:"routes,omitempty"`
}

type llmClient struct {
	Format     string `toml:"format"`
	BaseURL    string `toml:"base_url"`
	MaxRetries *int   `toml:"max_retries,omitempty"`
}

type target struct {
	ID        string `toml:"id"`
	LLMClient string `toml:"llm_client"`
}

// route holds every route type's keys; each renderer fills its own.
type route struct {
	ID   string `toml:"id"`
	Type string `toml:"type"`
	// stage_router.
	CapableTarget       string   `toml:"capable_target,omitempty"`
	EfficientTarget     string   `toml:"efficient_target,omitempty"`
	Picker              string   `toml:"picker,omitempty"`
	ConfidenceThreshold *float64 `toml:"confidence_threshold,omitempty"`
	// llm_classifier.
	Mode             string `toml:"mode,omitempty"`
	ClassifierTarget string `toml:"classifier_target,omitempty"`
	// llm_classifier and random.
	Targets []string `toml:"targets,omitempty"`
	// random.
	Weights []int `toml:"weights,omitempty"`
	// llm_classifier.
	DefaultTarget       string       `toml:"default_target,omitempty"`
	Prompt              string       `toml:"prompt,omitempty,multiline"`
	ResponseSchema      string       `toml:"response_schema,omitempty,multiline"`
	SessionAffinity     *bool        `toml:"session_affinity,omitempty"`
	MessageHashFallback *bool        `toml:"message_hash_fallback,omitempty"`
	RecentTurnWindow    *int         `toml:"recent_turn_window,omitempty"`
	MaxOutputTokens     *int         `toml:"max_output_tokens,omitempty"`
	Policy              *routePolicy `toml:"policy,omitempty"`
}

type routePolicy struct {
	Type     string `toml:"type"`
	Selector string `toml:"selector"`
}

const (
	formatOpenAIChat = "openai_chat"
	// polypusClient is the Switchyard llm client every target calls Polypus through.
	polypusClient = "polypus"
	// fallbackClient is the llm client fallback targets use (no retries).
	fallbackClient = "polypus_fallback"
)

// render is Render before its error is typed at the package boundary.
func render(cfg config.RouterConfig, polypusBaseURL string) ([]byte, error) {
	polypusBaseURL = normalizePolypusBaseURL(polypusBaseURL)
	file := routesFile{
		SchemaVersion: 1,
		LLMClients:    map[string]llmClient{polypusClient: {Format: formatOpenAIChat, BaseURL: polypusBaseURL}},
		Targets:       map[string]target{},
		Routes:        map[string]route{},
	}
	if hasFallbackRouter(cfg) {
		// Fallback routes try the next target at once, so retrying a refused target only stalls.
		noRetries := 0
		file.LLMClients[fallbackClient] = llmClient{Format: formatOpenAIChat, BaseURL: polypusBaseURL, MaxRetries: &noRetries}
	}
	for name, router := range cfg.Routers {
		var err error
		switch router.Route.Type {
		case config.RoutePassthrough:
			// Polypus serves passthrough routers itself; Switchyard never sees them.
		case config.RouteStageRouter:
			addStageRouter(&file, name, router)
		case config.RouteLLMClassifier:
			err = addLLMClassifier(&file, name, router)
		case config.RouteFallback:
			addFallback(&file, name, router)
		default:
			err = fmt.Errorf("switchyard render: routers.%s: unsupported composed type %q", name, router.Route.Type)
		}
		if err != nil {
			return nil, err
		}
	}
	// Maps encode with sorted keys, so the file is the same for the same config.
	return toml.Marshal(file)
}

func hasFallbackRouter(cfg config.RouterConfig) bool {
	for _, r := range cfg.Routers {
		if r.Route.Type == config.RouteFallback {
			return true
		}
	}
	return false
}

// addFallback adds a random route weighted 1 on the first target and 0 on the rest:
// Switchyard always picks the first and keeps the others for request-local fallback.
func addFallback(file *routesFile, name string, router config.NamedRouter) {
	leaves := router.Route.FallbackTargets
	names := make([]string, 0, len(leaves))
	weights := make([]int, 0, len(leaves))
	for i, leaf := range leaves {
		t := config.FallbackTargetName(name, i)
		file.Targets[t] = target{ID: leaf, LLMClient: fallbackClient}
		names = append(names, t)
		weight := 0
		if i == 0 {
			weight = 1
		}
		weights = append(weights, weight)
	}
	file.Routes[name] = route{ID: config.RouterPublicID(name), Type: "random", Targets: names, Weights: weights}
}

func addStageRouter(file *routesFile, name string, router config.NamedRouter) {
	capable, efficient := config.StageTargetNames(name)
	file.Targets[capable] = target{ID: router.Route.Capable, LLMClient: polypusClient}
	file.Targets[efficient] = target{ID: router.Route.Efficient, LLMClient: polypusClient}
	threshold := router.Route.ConfidenceThreshold
	file.Routes[name] = route{
		ID:                  config.RouterPublicID(name),
		Type:                string(config.RouteStageRouter),
		CapableTarget:       capable,
		EfficientTarget:     efficient,
		Picker:              string(router.Route.Picker),
		ConfidenceThreshold: &threshold,
		RecentTurnWindow:    router.Route.RecentTurnWindow,
	}
}

func addLLMClassifier(file *routesFile, name string, router config.NamedRouter) error {
	r := router.Route
	if r.Mode != config.ClassifierModeCustom {
		return fmt.Errorf("switchyard render: routers.%s: llm_classifier mode %q unsupported", name, r.Mode)
	}
	classifier := config.ClassifierTargetName(name)
	file.Targets[classifier] = target{ID: r.Classifier, LLMClient: polypusClient}
	names := make([]string, 0, len(r.Targets))
	for _, t := range r.Targets {
		file.Targets[t.Name] = target{ID: t.Model, LLMClient: polypusClient}
		names = append(names, t.Name)
	}
	file.Routes[name] = route{
		ID:                  config.RouterPublicID(name),
		Type:                string(config.RouteLLMClassifier),
		Mode:                string(config.ClassifierModeCustom),
		ClassifierTarget:    classifier,
		Targets:             names,
		DefaultTarget:       r.DefaultTarget,
		Prompt:              r.Prompt,
		ResponseSchema:      r.ResponseSchema,
		SessionAffinity:     r.SessionAffinity,
		MessageHashFallback: r.MessageHashFallback,
		RecentTurnWindow:    r.RecentTurnWindow,
		MaxOutputTokens:     r.MaxOutputTokens,
		Policy:              &routePolicy{Type: string(r.PolicyType), Selector: r.PolicySelector},
	}
	return nil
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
	out, err := writeConfig(cfg, polypusBaseURL)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInternal), "switchyard.WriteConfig", "write routes")
	}
	return out, nil
}

// writeConfig is WriteConfig before its error is typed at the package boundary.
func writeConfig(cfg config.RouterConfig, polypusBaseURL string) (string, error) {
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
	// Atomic, so Switchyard never reads a half-written routes.toml.
	if err := statefile.WriteAtomic(path, out, 0o600); err != nil {
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
