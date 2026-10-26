package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RouterIDPrefix starts every public router model id.
const RouterIDPrefix = "router/"

// reservedBackendID cannot name a backend: router/<name> model ids would be ambiguous.
const reservedBackendID = "router"

// RouteType is how a named router picks its leaf.
type RouteType string

// Route types.
const (
	RoutePassthrough   RouteType = "passthrough"
	RouteStageRouter   RouteType = "stage_router"
	RouteLLMClassifier RouteType = "llm_classifier"
	RouteFallback      RouteType = "fallback"
)

// Picker is the order a stage_router tries its leaves in.
type Picker string

// Stage router pickers.
const (
	PickerEfficientFirst Picker = "efficient_first"
	PickerCapableFirst   Picker = "capable_first"
)

// ClassifierMode is how an llm_classifier route is configured.
type ClassifierMode string

// ClassifierModeCustom is the one supported llm_classifier mode.
const ClassifierModeCustom ClassifierMode = "custom"

// PolicyType is how an llm_classifier reads the judge's decision.
type PolicyType string

// PolicyTargetSelector reads the chosen target from a JSON pointer.
const PolicyTargetSelector PolicyType = "target_selector"

// NamedRouter is one configured router/<name> entry.
type NamedRouter struct {
	Name       string
	Capability Capability
	Route      RouterRoute
}

// ClassifierTarget is one named leaf under an llm_classifier custom route.
type ClassifierTarget struct {
	Name  string
	Model string
}

// RouterRoute holds passthrough or composed routing policy.
type RouterRoute struct {
	Type                RouteType
	Target              string
	Capable             string
	Efficient           string
	Picker              Picker
	ConfidenceThreshold float64
	RecentTurnWindow    *int
	Mode                ClassifierMode
	Classifier          string
	Targets             []ClassifierTarget
	DefaultTarget       string
	Prompt              string
	ResponseSchema      string
	PolicyType          PolicyType
	PolicySelector      string
	SessionAffinity     *bool
	MessageHashFallback *bool
	MaxOutputTokens     *int
	// FallbackTargets lists fallback route leaves in try order (first is primary).
	FallbackTargets []string
}

// SwitchyardConfig points at the always-on Switchyard process.
type SwitchyardConfig struct {
	BaseURL    string
	ConfigPath string
}

// RouterPublicID returns the OpenAI model id for a router yaml key.
func RouterPublicID(name string) string {
	return RouterIDPrefix + strings.TrimSpace(name)
}

// IsComposed reports whether the route is handled by Switchyard.
func (r NamedRouter) IsComposed() bool {
	return r.Route.Type == RouteStageRouter || r.Route.Type == RouteLLMClassifier || r.Route.Type == RouteFallback
}

// FallbackTargetName returns the Switchyard target table name for a fallback leaf.
func FallbackTargetName(routerName string, index int) string {
	return fmt.Sprintf("%s_%d", strings.TrimSpace(routerName), index)
}

// StageTargetNames returns the Switchyard target names of a stage_router's capable and
// efficient leaves.
func StageTargetNames(routerName string) (capable, efficient string) {
	n := strings.TrimSpace(routerName)
	return n + "_capable", n + "_efficient"
}

// ClassifierTargetName returns the Switchyard target table name for the route judge.
func ClassifierTargetName(routerName string) string {
	return strings.TrimSpace(routerName) + "_classifier"
}

type switchyardFile struct {
	BaseURL    string `yaml:"base_url"`
	ConfigPath string `yaml:"config_path"`
}

type namedRouterFile struct {
	Capability string          `yaml:"capability"`
	Route      routerRouteFile `yaml:"route"`
}

type classifierRoutePolicyFile struct {
	Type     string `yaml:"type"`
	Selector string `yaml:"selector"`
}

type routerRouteFile struct {
	Type                string                     `yaml:"type"`
	Target              string                     `yaml:"target"`
	Capable             string                     `yaml:"capable"`
	Efficient           string                     `yaml:"efficient"`
	Picker              string                     `yaml:"picker"`
	ConfidenceThreshold *float64                   `yaml:"confidence_threshold"`
	RecentTurnWindow    *int                       `yaml:"recent_turn_window"`
	Mode                string                     `yaml:"mode"`
	Classifier          string                     `yaml:"classifier"`
	Targets             routeTargetsFile           `yaml:"targets"`
	DefaultTarget       string                     `yaml:"default_target"`
	Prompt              string                     `yaml:"prompt"`
	ResponseSchema      string                     `yaml:"response_schema"`
	Policy              *classifierRoutePolicyFile `yaml:"policy"`
	SessionAffinity     *bool                      `yaml:"session_affinity"`
	MessageHashFallback *bool                      `yaml:"message_hash_fallback"`
	MaxOutputTokens     *int                       `yaml:"max_output_tokens"`
}

// routeTargetsFile accepts a mapping (llm_classifier: target name to leaf) or a
// sequence (fallback: leaves in try order).
type routeTargetsFile struct {
	Named   map[string]string
	Ordered []string
}

func (t *routeTargetsFile) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.MappingNode:
		return value.Decode(&t.Named)
	case yaml.SequenceNode:
		return value.Decode(&t.Ordered)
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
		return fmt.Errorf("targets must be a mapping (llm_classifier) or a list (fallback)")
	default:
		return fmt.Errorf("targets must be a mapping (llm_classifier) or a list (fallback)")
	}
}

func (t routeTargetsFile) empty() bool {
	return len(t.Named) == 0 && len(t.Ordered) == 0
}

func (r *routerRouteFile) UnmarshalYAML(value *yaml.Node) error {
	type plain routerRouteFile
	return decodeStrictNode(value, (*plain)(r))
}

func (r *namedRouterFile) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Capability string    `yaml:"capability"`
		Route      yaml.Node `yaml:"route"`
	}
	var p plain
	if err := decodeStrictNode(value, &p); err != nil {
		return err
	}
	r.Capability = p.Capability
	var route routerRouteFile
	if err := decodeStrictNode(&p.Route, &route); err != nil {
		return err
	}
	r.Route = route
	return nil
}

func decodeStrictNode(node *yaml.Node, out any) error {
	if node == nil {
		return nil
	}
	raw, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	return dec.Decode(out)
}

func defaultSwitchyardConfig() SwitchyardConfig {
	path, err := DefaultSwitchyardRoutesPath()
	if err != nil {
		path = ""
	}
	return SwitchyardConfig{
		BaseURL:    "http://127.0.0.1:4000",
		ConfigPath: path,
	}
}

func mergeSwitchyardFile(file switchyardFile) SwitchyardConfig {
	var cfg SwitchyardConfig
	if v := strings.TrimSpace(file.BaseURL); v != "" {
		cfg.BaseURL = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(file.ConfigPath); v != "" {
		cfg.ConfigPath = v
	}
	return cfg
}

func parseNamedRouters(raw map[string]namedRouterFile) (map[string]NamedRouter, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]NamedRouter, len(raw))
	for name, entry := range raw {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("router: routers key must not be empty")
		}
		if strings.Contains(name, "/") {
			return nil, fmt.Errorf("router: routers.%s: name must not contain '/'", name)
		}
		if err := validateRouterName(name); err != nil {
			return nil, fmt.Errorf("router: routers.%s: %w", name, err)
		}
		capability := Capability(strings.TrimSpace(entry.Capability))
		if capability == "" {
			return nil, fmt.Errorf("router: routers.%s.capability required", name)
		}
		route, err := parseRouterRoute(name, entry.Route)
		if err != nil {
			return nil, err
		}
		out[name] = NamedRouter{
			Name:       name,
			Capability: capability,
			Route:      route,
		}
	}
	return out, nil
}

func parseRouterRoute(name string, file routerRouteFile) (RouterRoute, error) {
	routeType := RouteType(strings.TrimSpace(file.Type))
	if routeType == "" {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.type required", name)
	}
	switch routeType {
	case RoutePassthrough:
		if err := rejectPassthroughRouteExtras(name, file); err != nil {
			return RouterRoute{}, err
		}
		target := strings.TrimSpace(file.Target)
		if target == "" {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.target required for passthrough", name)
		}
		return RouterRoute{Type: RoutePassthrough, Target: target}, nil
	case RouteStageRouter:
		if err := rejectStageRouteExtras(name, file); err != nil {
			return RouterRoute{}, err
		}
		capable := strings.TrimSpace(file.Capable)
		efficient := strings.TrimSpace(file.Efficient)
		picker := Picker(strings.TrimSpace(file.Picker))
		if capable == "" {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.capable required for stage_router", name)
		}
		if efficient == "" {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.efficient required for stage_router", name)
		}
		if picker == "" {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.picker required for stage_router", name)
		}
		if picker != PickerEfficientFirst && picker != PickerCapableFirst {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.picker must be efficient_first or capable_first", name)
		}
		if file.ConfidenceThreshold == nil {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.confidence_threshold required for stage_router", name)
		}
		threshold := *file.ConfidenceThreshold
		if threshold < 0 || threshold > 1 {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.confidence_threshold must be in [0, 1]", name)
		}
		if file.RecentTurnWindow != nil && *file.RecentTurnWindow < 1 {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.recent_turn_window must be >= 1", name)
		}
		return RouterRoute{
			Type:                RouteStageRouter,
			Capable:             capable,
			Efficient:           efficient,
			Picker:              picker,
			ConfidenceThreshold: threshold,
			RecentTurnWindow:    file.RecentTurnWindow,
		}, nil
	case RouteLLMClassifier:
		if err := rejectClassifierRouteExtras(name, file); err != nil {
			return RouterRoute{}, err
		}
		return parseLLMClassifierRoute(name, file)
	case RouteFallback:
		if err := rejectFallbackRouteExtras(name, file); err != nil {
			return RouterRoute{}, err
		}
		return parseFallbackRoute(name, file)
	default:
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.type %q unsupported (v1: passthrough, stage_router, llm_classifier, fallback)", name, routeType)
	}
}

// parseFallbackRoute reads the ordered leaves of a fallback route. Leaves must be
// distinct, because Switchyard excludes a failed target by its model id.
func parseFallbackRoute(name string, file routerRouteFile) (RouterRoute, error) {
	if len(file.Targets.Ordered) < 2 {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.targets requires at least 2 entries for fallback", name)
	}
	leaves := make([]string, 0, len(file.Targets.Ordered))
	seen := make(map[string]struct{}, len(file.Targets.Ordered))
	for i, raw := range file.Targets.Ordered {
		leaf := strings.TrimSpace(raw)
		if leaf == "" {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.targets[%d] must not be empty", name, i)
		}
		if _, dup := seen[leaf]; dup {
			return RouterRoute{}, fmt.Errorf("router: routers.%s.route.targets[%d] repeats %q", name, i, leaf)
		}
		seen[leaf] = struct{}{}
		leaves = append(leaves, leaf)
	}
	return RouterRoute{Type: RouteFallback, FallbackTargets: leaves}, nil
}

// parseClassifierTargets reads the named targets in sorted order. seen holds every
// target name, for checking default_target.
func parseClassifierTargets(name string, named map[string]string) ([]ClassifierTarget, map[string]struct{}, error) {
	keys := make([]string, 0, len(named))
	for k := range named {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	targets := make([]ClassifierTarget, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	classifierTable := ClassifierTargetName(name)
	for _, rawKey := range keys {
		key := strings.TrimSpace(rawKey)
		if key == "" {
			return nil, nil, fmt.Errorf("router: routers.%s.route.targets key must not be empty", name)
		}
		if err := validateRouterName(key); err != nil {
			return nil, nil, fmt.Errorf("router: routers.%s.route.targets.%s: %w", name, key, err)
		}
		if key == classifierTable {
			return nil, nil, fmt.Errorf("router: routers.%s.route.targets.%s collides with classifier target name", name, key)
		}
		if _, ok := seen[key]; ok {
			return nil, nil, fmt.Errorf("router: routers.%s.route.targets.%s duplicate", name, key)
		}
		seen[key] = struct{}{}
		model := strings.TrimSpace(named[rawKey])
		if model == "" {
			return nil, nil, fmt.Errorf("router: routers.%s.route.targets.%s leaf required", name, key)
		}
		targets = append(targets, ClassifierTarget{Name: key, Model: model})
	}
	return targets, seen, nil
}

// classifierSelector checks the target_selector policy and returns its JSON Pointer.
func classifierSelector(name string, policy *classifierRoutePolicyFile) (string, error) {
	if policy == nil {
		return "", fmt.Errorf("router: routers.%s.route.policy required for llm_classifier custom", name)
	}
	policyType := PolicyType(strings.TrimSpace(policy.Type))
	if policyType == "" {
		return "", fmt.Errorf("router: routers.%s.route.policy.type required for llm_classifier custom", name)
	}
	if policyType != PolicyTargetSelector {
		return "", fmt.Errorf("router: routers.%s.route.policy.type %q unsupported (v1: target_selector)", name, policyType)
	}
	selector := strings.TrimSpace(policy.Selector)
	if selector == "" {
		return "", fmt.Errorf("router: routers.%s.route.policy.selector required for llm_classifier custom", name)
	}
	if !strings.HasPrefix(selector, "/") {
		return "", fmt.Errorf("router: routers.%s.route.policy.selector must be a JSON Pointer starting with /", name)
	}
	return selector, nil
}

// validateClassifierKnobs checks the optional numeric and affinity settings.
func validateClassifierKnobs(name string, file routerRouteFile) error {
	if file.RecentTurnWindow != nil && *file.RecentTurnWindow < 0 {
		return fmt.Errorf("router: routers.%s.route.recent_turn_window must be >= 0", name)
	}
	if file.MaxOutputTokens != nil && *file.MaxOutputTokens < 1 {
		return fmt.Errorf("router: routers.%s.route.max_output_tokens must be >= 1", name)
	}
	if file.MessageHashFallback != nil && *file.MessageHashFallback &&
		(file.SessionAffinity == nil || !*file.SessionAffinity) {
		return fmt.Errorf("router: routers.%s.route.message_hash_fallback requires session_affinity: true", name)
	}
	return nil
}

func parseLLMClassifierRoute(name string, file routerRouteFile) (RouterRoute, error) {
	mode := ClassifierMode(strings.TrimSpace(file.Mode))
	if mode == "" {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.mode required for llm_classifier", name)
	}
	if mode != ClassifierModeCustom {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.mode %q unsupported (v1: custom)", name, mode)
	}
	classifier := strings.TrimSpace(file.Classifier)
	if classifier == "" {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.classifier required for llm_classifier custom", name)
	}
	if len(file.Targets.Named) < 2 {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.targets requires at least 2 entries for llm_classifier custom", name)
	}
	targets, seen, err := parseClassifierTargets(name, file.Targets.Named)
	if err != nil {
		return RouterRoute{}, err
	}
	defaultTarget := strings.TrimSpace(file.DefaultTarget)
	if defaultTarget == "" {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.default_target required for llm_classifier custom", name)
	}
	if _, ok := seen[defaultTarget]; !ok {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.default_target %q must be a key in targets", name, defaultTarget)
	}
	prompt := strings.TrimSpace(file.Prompt)
	if prompt == "" {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.prompt required for llm_classifier custom", name)
	}
	if strings.Contains(prompt, "{{RESPONSE_SCHEMA}}") {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.prompt must not contain {{RESPONSE_SCHEMA}}", name)
	}
	schema := strings.TrimSpace(file.ResponseSchema)
	if schema == "" {
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.response_schema required for llm_classifier custom", name)
	}
	if err := validateClassifierResponseSchema(name, schema); err != nil {
		return RouterRoute{}, err
	}
	selector, err := classifierSelector(name, file.Policy)
	if err != nil {
		return RouterRoute{}, err
	}
	if err := validateClassifierKnobs(name, file); err != nil {
		return RouterRoute{}, err
	}
	return RouterRoute{
		Type:                RouteLLMClassifier,
		Mode:                ClassifierModeCustom,
		Classifier:          classifier,
		Targets:             targets,
		DefaultTarget:       defaultTarget,
		Prompt:              prompt,
		ResponseSchema:      schema,
		PolicyType:          PolicyTargetSelector, // classifierSelector accepts no other type.
		PolicySelector:      selector,
		RecentTurnWindow:    file.RecentTurnWindow,
		SessionAffinity:     file.SessionAffinity,
		MessageHashFallback: file.MessageHashFallback,
		MaxOutputTokens:     file.MaxOutputTokens,
	}, nil
}

func validateClassifierResponseSchema(name, schema string) error {
	var raw any
	if err := json.Unmarshal([]byte(schema), &raw); err != nil {
		return fmt.Errorf("router: routers.%s.route.response_schema must be valid JSON: %w", name, err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("router: routers.%s.route.response_schema must be a JSON object", name)
	}
	if len(obj) == 0 {
		return fmt.Errorf("router: routers.%s.route.response_schema must not be an empty object", name)
	}
	return nil
}

func validateRouters(cfg *RouterConfig) error {
	if _, ok := cfg.Backends[reservedBackendID]; ok {
		return fmt.Errorf("router: backend id %q is reserved", reservedBackendID)
	}
	// Switchyard target names a router emits must be unique across routers.
	targetOwners := make(map[string]string)
	for name, router := range cfg.Routers {
		if router.Capability != CapChat {
			return fmt.Errorf("router: routers.%s.capability must be chat (v1)", name)
		}
		leaves, err := routeLeaves(name, router.Route)
		if err != nil {
			return err
		}
		for _, l := range leaves {
			if err := validateChatLeaf(cfg, l.leaf, l.field); err != nil {
				return err
			}
			if l.target == "" {
				continue
			}
			if other, ok := targetOwners[l.target]; ok && other != name {
				return fmt.Errorf("router: %s Switchyard target %q conflicts with routers.%s (emitted target names must be unique)", l.field, l.target, other)
			}
			targetOwners[l.target] = name
		}
	}
	return nil
}

// fillSwitchyardDefaults gives composed routers the default Switchyard URL and
// routes.toml path when the config names none.
func fillSwitchyardDefaults(cfg *RouterConfig) {
	if !cfg.HasComposedRouters() {
		return
	}
	if cfg.Switchyard.BaseURL == "" {
		cfg.Switchyard.BaseURL = defaultSwitchyardConfig().BaseURL
	}
	if cfg.Switchyard.ConfigPath == "" {
		cfg.Switchyard.ConfigPath = defaultSwitchyardConfig().ConfigPath
	}
}

// routeLeaf is one chat leaf a route dials.
type routeLeaf struct {
	field  string // config path, for errors.
	leaf   string // backend_id/downstream.
	target string // Switchyard target name it emits; empty for passthrough.
}

// routeLeaves lists the leaves of route by type, with the target names each emits.
func routeLeaves(name string, route RouterRoute) ([]routeLeaf, error) {
	field := func(suffix string) string { return fmt.Sprintf("routers.%s.route.%s", name, suffix) }
	switch route.Type {
	case RoutePassthrough:
		return []routeLeaf{{field: field("target"), leaf: route.Target}}, nil
	case RouteStageRouter:
		capable, efficient := StageTargetNames(name)
		return []routeLeaf{
			{field: field("capable"), leaf: route.Capable, target: capable},
			{field: field("efficient"), leaf: route.Efficient, target: efficient},
		}, nil
	case RouteLLMClassifier:
		out := []routeLeaf{{field: field("classifier"), leaf: route.Classifier, target: ClassifierTargetName(name)}}
		for _, t := range route.Targets {
			out = append(out, routeLeaf{field: field("targets." + t.Name), leaf: t.Model, target: t.Name})
		}
		return out, nil
	case RouteFallback:
		out := make([]routeLeaf, 0, len(route.FallbackTargets))
		for i, leaf := range route.FallbackTargets {
			out = append(out, routeLeaf{field: field(fmt.Sprintf("targets[%d]", i)), leaf: leaf, target: FallbackTargetName(name, i)})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("router: routers.%s.route.type %q unsupported", name, route.Type)
	}
}

func validateChatLeaf(cfg *RouterConfig, leaf, field string) error {
	backendID, model, err := splitLeafModel(leaf)
	if err != nil {
		return fmt.Errorf("router: %s: %w", field, err)
	}
	if backendID == reservedBackendID {
		return fmt.Errorf("router: %s: backend id router forbidden", field)
	}
	b, ok := cfg.Backends[backendID]
	if !ok {
		return fmt.Errorf("router: %s: backend %q not found", field, backendID)
	}
	if !b.HasCapability(CapChat) {
		return fmt.Errorf("router: %s: backend %q lacks chat capability", field, backendID)
	}
	if !b.IsModelAllowed(model) && !b.IsModelAllowed(leaf) {
		return fmt.Errorf("router: %s: leaf %q not allowed by backend allow-list", field, leaf)
	}
	return nil
}

func splitLeafModel(leaf string) (backendID, model string, err error) {
	leaf = strings.TrimSpace(leaf)
	i := strings.Index(leaf, "/")
	if i <= 0 {
		return "", "", fmt.Errorf("leaf %q must be backend_id/downstream", leaf)
	}
	backendID = leaf[:i]
	model = strings.TrimSpace(leaf[i+1:])
	if model == "" {
		return "", "", fmt.Errorf("leaf %q missing downstream model", leaf)
	}
	return backendID, model, nil
}

func rejectPassthroughRouteExtras(name string, file routerRouteFile) error {
	if err := rejectStageOnlyFields(name, RoutePassthrough, file); err != nil {
		return err
	}
	return rejectClassifierOnlyFields(name, RoutePassthrough, file)
}

// rejectFallbackRouteExtras allows only an ordered targets list on a fallback route.
func rejectFallbackRouteExtras(name string, file routerRouteFile) error {
	if len(file.Targets.Named) > 0 {
		return fmt.Errorf("router: routers.%s.route.targets must be a list for fallback", name)
	}
	if strings.TrimSpace(file.Target) != "" {
		return fmt.Errorf("router: routers.%s.route.target not allowed for fallback", name)
	}
	rest := file
	rest.Targets = routeTargetsFile{}
	if err := rejectStageOnlyFields(name, RouteFallback, rest); err != nil {
		return err
	}
	return rejectClassifierOnlyFields(name, RouteFallback, rest)
}

func rejectStageOnlyFields(name string, routeType RouteType, file routerRouteFile) error {
	if strings.TrimSpace(file.Capable) != "" {
		return fmt.Errorf("router: routers.%s.route.capable not allowed for %s", name, routeType)
	}
	if strings.TrimSpace(file.Efficient) != "" {
		return fmt.Errorf("router: routers.%s.route.efficient not allowed for %s", name, routeType)
	}
	if strings.TrimSpace(file.Picker) != "" {
		return fmt.Errorf("router: routers.%s.route.picker not allowed for %s", name, routeType)
	}
	if file.ConfidenceThreshold != nil {
		return fmt.Errorf("router: routers.%s.route.confidence_threshold not allowed for %s", name, routeType)
	}
	if file.RecentTurnWindow != nil {
		return fmt.Errorf("router: routers.%s.route.recent_turn_window not allowed for %s", name, routeType)
	}
	return nil
}

func rejectStageRouteExtras(name string, file routerRouteFile) error {
	if strings.TrimSpace(file.Target) != "" {
		return fmt.Errorf("router: routers.%s.route.target not allowed for stage_router", name)
	}
	return rejectClassifierOnlyFields(name, RouteStageRouter, file)
}

func rejectClassifierRouteExtras(name string, file routerRouteFile) error {
	if len(file.Targets.Ordered) > 0 {
		return fmt.Errorf("router: routers.%s.route.targets must be a mapping for llm_classifier", name)
	}
	if strings.TrimSpace(file.Target) != "" {
		return fmt.Errorf("router: routers.%s.route.target not allowed for llm_classifier", name)
	}
	if strings.TrimSpace(file.Capable) != "" {
		return fmt.Errorf("router: routers.%s.route.capable not allowed for llm_classifier", name)
	}
	if strings.TrimSpace(file.Efficient) != "" {
		return fmt.Errorf("router: routers.%s.route.efficient not allowed for llm_classifier", name)
	}
	if strings.TrimSpace(file.Picker) != "" {
		return fmt.Errorf("router: routers.%s.route.picker not allowed for llm_classifier", name)
	}
	if file.ConfidenceThreshold != nil {
		return fmt.Errorf("router: routers.%s.route.confidence_threshold not allowed for llm_classifier", name)
	}
	return nil
}

func rejectClassifierOnlyFields(name string, routeType RouteType, file routerRouteFile) error {
	if strings.TrimSpace(file.Mode) != "" {
		return fmt.Errorf("router: routers.%s.route.mode not allowed for %s", name, routeType)
	}
	if strings.TrimSpace(file.Classifier) != "" {
		return fmt.Errorf("router: routers.%s.route.classifier not allowed for %s", name, routeType)
	}
	if !file.Targets.empty() {
		return fmt.Errorf("router: routers.%s.route.targets not allowed for %s", name, routeType)
	}
	if strings.TrimSpace(file.DefaultTarget) != "" {
		return fmt.Errorf("router: routers.%s.route.default_target not allowed for %s", name, routeType)
	}
	if strings.TrimSpace(file.Prompt) != "" {
		return fmt.Errorf("router: routers.%s.route.prompt not allowed for %s", name, routeType)
	}
	if strings.TrimSpace(file.ResponseSchema) != "" {
		return fmt.Errorf("router: routers.%s.route.response_schema not allowed for %s", name, routeType)
	}
	if file.Policy != nil {
		return fmt.Errorf("router: routers.%s.route.policy not allowed for %s", name, routeType)
	}
	if file.SessionAffinity != nil {
		return fmt.Errorf("router: routers.%s.route.session_affinity not allowed for %s", name, routeType)
	}
	if file.MessageHashFallback != nil {
		return fmt.Errorf("router: routers.%s.route.message_hash_fallback not allowed for %s", name, routeType)
	}
	if file.MaxOutputTokens != nil {
		return fmt.Errorf("router: routers.%s.route.max_output_tokens not allowed for %s", name, routeType)
	}
	return nil
}

func validateRouterName(name string) error {
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	for i, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return fmt.Errorf("name %q contains invalid character %q (use letters, digits, _, -)", name, r)
		}
		if i == 0 && r >= '0' && r <= '9' {
			return fmt.Errorf("name %q must start with a letter", name)
		}
	}
	return nil
}

// EffectiveSwitchyardBaseURL returns the Switchyard HTTP base URL (configured or default).
func (c RouterConfig) EffectiveSwitchyardBaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(c.Switchyard.BaseURL), "/"); v != "" {
		return v
	}
	return defaultSwitchyardConfig().BaseURL
}

// ResolveSwitchyardConfigPath returns the generated Switchyard TOML path.
func (c RouterConfig) ResolveSwitchyardConfigPath() (string, error) {
	if v := strings.TrimSpace(c.Switchyard.ConfigPath); v != "" {
		return v, nil
	}
	return DefaultSwitchyardRoutesPath()
}

// HasComposedRouters reports whether any router uses Switchyard.
func (c RouterConfig) HasComposedRouters() bool {
	for _, r := range c.Routers {
		if r.IsComposed() {
			return true
		}
	}
	return false
}

// LookupNamedRouter finds a router by bare yaml key or public router/<name> id.
func (c RouterConfig) LookupNamedRouter(name string) (NamedRouter, bool) {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, RouterIDPrefix)
	if name == "" {
		return NamedRouter{}, false
	}
	r, ok := c.Routers[name]
	return r, ok
}
