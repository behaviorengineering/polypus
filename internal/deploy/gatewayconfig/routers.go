package gatewayconfig

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"gopkg.in/yaml.v3"
)

// RouterTarget is one chat router to smoke after a deploy.
type RouterTarget struct {
	// Model is the public id, router/<name>.
	Model string
	// Composed routers (stage_router, llm_classifier, fallback) run through
	// Switchyard, which reports the leaf it picked by name; a passthrough router
	// reports only the upstream model.
	Composed bool
	// Leaves are the backend/model ids the router can answer with.
	Leaves []string
}

type routersFile struct {
	Routers map[string]struct {
		Capability string `yaml:"capability"`
		Route      struct {
			Type      config.RouteType `yaml:"type"`
			Target    string           `yaml:"target"`
			Capable   string           `yaml:"capable"`
			Efficient string           `yaml:"efficient"`
			Targets   yaml.Node        `yaml:"targets"`
		} `yaml:"route"`
	} `yaml:"routers"`
}

// HasRouters reports whether the gateway YAML defines at least one router under its
// top-level routers: key. A commented, indented or empty routers: does not count.
func HasRouters(path string) (bool, error) {
	f, err := loadRouters(path)
	if err != nil {
		return false, err
	}
	return len(f.Routers) > 0, nil
}

func loadRouters(path string) (*routersFile, error) {
	data, err := readGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	var f routersFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("gatewayconfig: routers in %q: %w", path, err)
	}
	return &f, nil
}

// HasComposedRouters reports whether the gateway YAML has a router of any
// capability that Switchyard serves (stage_router, llm_classifier or fallback).
// The gateway answers passthrough routers itself, so they need no Switchyard.
func HasComposedRouters(path string) (bool, error) {
	f, err := loadRouters(path)
	if err != nil {
		return false, err
	}
	for _, r := range f.Routers {
		switch r.Route.Type {
		case config.RouteStageRouter, config.RouteLLMClassifier, config.RouteFallback:
			return true, nil
		case config.RoutePassthrough:
		}
	}
	return false, nil
}

// RouterSmokeTargets returns the gateway YAML's chat routers in name order, each
// with the leaves it can answer with.
func RouterSmokeTargets(path string) ([]RouterTarget, error) {
	f, err := loadRouters(path)
	if err != nil {
		return nil, err
	}
	var out []RouterTarget
	for _, name := range slices.Sorted(maps.Keys(f.Routers)) {
		r := f.Routers[name]
		if c := strings.TrimSpace(r.Capability); c != "" && c != "chat" {
			continue
		}
		target := RouterTarget{Model: "router/" + name, Composed: r.Route.Type != config.RoutePassthrough}
		switch r.Route.Type {
		case config.RoutePassthrough:
			target.Leaves = []string{r.Route.Target}
		case config.RouteStageRouter:
			target.Leaves = []string{r.Route.Capable, r.Route.Efficient}
		case config.RouteFallback, config.RouteLLMClassifier:
			if target.Leaves, err = routeTargets(&r.Route.Targets); err != nil {
				return nil, fmt.Errorf("gatewayconfig: router %s: %w", name, err)
			}
		default:
			return nil, fmt.Errorf("gatewayconfig: router %s: unknown route type %q", name, r.Route.Type)
		}
		out = append(out, target)
	}
	return out, nil
}

// routeTargets reads targets: a list (fallback, in try order) or a map of target
// names to leaves (llm_classifier, in name order).
func routeTargets(node *yaml.Node) ([]string, error) {
	switch node.Kind {
	case yaml.SequenceNode:
		var leaves []string
		if err := node.Decode(&leaves); err != nil {
			return nil, err
		}
		return leaves, nil
	case yaml.MappingNode:
		var named map[string]string
		if err := node.Decode(&named); err != nil {
			return nil, err
		}
		leaves := make([]string, 0, len(named))
		for _, k := range slices.Sorted(maps.Keys(named)) {
			leaves = append(leaves, named[k])
		}
		return leaves, nil
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
		return nil, fmt.Errorf("targets must be a list or a map")
	default:
		return nil, fmt.Errorf("targets must be a list or a map")
	}
}
