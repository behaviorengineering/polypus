package config

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	RouterIDPrefix       = "router/"
	RoutePassthrough     = "passthrough"
	RouteStageRouter     = "stage_router"
	PickerEfficientFirst = "efficient_first"
	PickerCapableFirst   = "capable_first"
)

// NamedRouter is one configured router/<name> entry.
type NamedRouter struct {
	Name       string
	Capability Capability
	Route      RouterRoute
}

// RouterRoute holds passthrough or composed routing policy.
type RouterRoute struct {
	Type                string
	Target              string
	Capable             string
	Efficient           string
	Picker              string
	ConfidenceThreshold float64
	RecentTurnWindow    *int
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
	return r.Route.Type == RouteStageRouter
}

type switchyardFile struct {
	BaseURL    string `yaml:"base_url"`
	ConfigPath string `yaml:"config_path"`
}

type namedRouterFile struct {
	Capability string          `yaml:"capability"`
	Route      routerRouteFile `yaml:"route"`
}

type routerRouteFile struct {
	Type                string   `yaml:"type"`
	Target              string   `yaml:"target"`
	Capable             string   `yaml:"capable"`
	Efficient           string   `yaml:"efficient"`
	Picker              string   `yaml:"picker"`
	ConfidenceThreshold *float64 `yaml:"confidence_threshold"`
	RecentTurnWindow    *int     `yaml:"recent_turn_window"`
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
	routeType := strings.TrimSpace(file.Type)
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
		picker := strings.TrimSpace(file.Picker)
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
	default:
		return RouterRoute{}, fmt.Errorf("router: routers.%s.route.type %q unsupported (v1: passthrough, stage_router)", name, routeType)
	}
}

func validateRouters(cfg *RouterConfig) error {
	if _, ok := cfg.Backends["router"]; ok {
		return fmt.Errorf("router: backend id %q is reserved", "router")
	}
	for name, router := range cfg.Routers {
		if router.Capability != CapChat {
			return fmt.Errorf("router: routers.%s.capability must be chat (v1)", name)
		}
		switch router.Route.Type {
		case RoutePassthrough:
			if err := validateChatLeaf(cfg, router.Route.Target, fmt.Sprintf("routers.%s.route.target", name)); err != nil {
				return err
			}
		case RouteStageRouter:
			if err := validateChatLeaf(cfg, router.Route.Capable, fmt.Sprintf("routers.%s.route.capable", name)); err != nil {
				return err
			}
			if err := validateChatLeaf(cfg, router.Route.Efficient, fmt.Sprintf("routers.%s.route.efficient", name)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("router: routers.%s.route.type %q unsupported", name, router.Route.Type)
		}
	}
	if cfg.Switchyard.BaseURL == "" && cfg.HasComposedRouters() {
		cfg.Switchyard.BaseURL = defaultSwitchyardConfig().BaseURL
	}
	if cfg.Switchyard.ConfigPath == "" && cfg.HasComposedRouters() {
		def := defaultSwitchyardConfig()
		cfg.Switchyard.ConfigPath = def.ConfigPath
	}
	return nil
}

func validateChatLeaf(cfg *RouterConfig, leaf, field string) error {
	backendID, model, err := splitLeafModel(leaf)
	if err != nil {
		return fmt.Errorf("router: %s: %w", field, err)
	}
	if backendID == "router" {
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
	if strings.TrimSpace(file.Capable) != "" {
		return fmt.Errorf("router: routers.%s.route.capable not allowed for passthrough", name)
	}
	if strings.TrimSpace(file.Efficient) != "" {
		return fmt.Errorf("router: routers.%s.route.efficient not allowed for passthrough", name)
	}
	if strings.TrimSpace(file.Picker) != "" {
		return fmt.Errorf("router: routers.%s.route.picker not allowed for passthrough", name)
	}
	if file.ConfidenceThreshold != nil {
		return fmt.Errorf("router: routers.%s.route.confidence_threshold not allowed for passthrough", name)
	}
	if file.RecentTurnWindow != nil {
		return fmt.Errorf("router: routers.%s.route.recent_turn_window not allowed for passthrough", name)
	}
	return nil
}

func rejectStageRouteExtras(name string, file routerRouteFile) error {
	if strings.TrimSpace(file.Target) != "" {
		return fmt.Errorf("router: routers.%s.route.target not allowed for stage_router", name)
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
	if strings.HasPrefix(name, RouterIDPrefix) {
		name = strings.TrimPrefix(name, RouterIDPrefix)
	}
	if name == "" {
		return NamedRouter{}, false
	}
	r, ok := c.Routers[name]
	return r, ok
}
