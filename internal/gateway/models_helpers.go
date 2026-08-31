package gateway

import (
	"fmt"
	"sort"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

// backendInventory is one backend's fetched model list (I/O result).
type backendInventory struct {
	backend string
	models  []openaiModel
}

// routerCatalogModels returns public router/<name> entries for tool-facing lists.
func routerCatalogModels(cfg config.RouterConfig) []openaiModel {
	if len(cfg.Routers) == 0 {
		return nil
	}
	names := make([]string, 0, len(cfg.Routers))
	for name := range cfg.Routers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]openaiModel, 0, len(names))
	for _, name := range names {
		out = append(out, openaiModel{
			ID:      config.RouterPublicID(name),
			Object:  "model",
			Created: 0,
			OwnedBy: "polypus",
		})
	}
	return out
}

// mergeBackendInventories dedupes backend inventories into by-id, applying
// allow-list (when not inventory view) and bare default-backend aliases.
func mergeBackendInventories(cfg config.RouterConfig, asInventory bool, results []backendInventory) map[string]openaiModel {
	byID := make(map[string]openaiModel)
	for _, res := range results {
		if res.backend == "" {
			continue
		}
		b := cfg.Backends[res.backend]
		for _, m := range res.models {
			if !asInventory && !b.IsModelAllowed(m.ID) {
				continue
			}
			byID[m.ID] = m
		}
		if !isDefaultBackend(cfg, res.backend) {
			continue
		}
		for _, m := range res.models {
			if !asInventory && !b.IsModelAllowed(m.ID) {
				continue
			}
			down := config.NormalizeDownstream(res.backend, m.ID)
			if down == "" || down == m.ID {
				continue
			}
			if _, ok := byID[down]; ok {
				continue
			}
			byID[down] = openaiModel{
				ID:      down,
				Object:  "model",
				Created: m.Created,
				OwnedBy: firstNonEmpty(m.OwnedBy, res.backend),
			}
		}
	}
	return byID
}

// mergeSeedModels adds env speech seeds that are allowed and not already present.
func mergeSeedModels(cfg config.RouterConfig, asInventory bool, ids []string, byID map[string]openaiModel, seeds []openaiModel) {
	if byID == nil {
		return
	}
	for _, seed := range seeds {
		b, ok := cfg.Backends[seed.OwnedBy]
		if !ok {
			for _, id := range ids {
				if strings.HasPrefix(seed.ID, id+"/") || seed.OwnedBy == id {
					b = cfg.Backends[id]
					ok = true
					break
				}
			}
		}
		if ok && !asInventory && !b.IsModelAllowed(seed.ID) {
			continue
		}
		if _, exists := byID[seed.ID]; !exists {
			byID[seed.ID] = seed
		}
	}
}

// finalizeModelList appends catalog + map values and sorts by id.
func finalizeModelList(catalog []openaiModel, byID map[string]openaiModel) []openaiModel {
	out := make([]openaiModel, 0, len(catalog)+len(byID))
	out = append(out, catalog...)
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// validateNamedRouterForChat checks capability and vision before route dispatch.
// Returns a client-facing error message, or empty when ok.
func validateNamedRouterForChat(nr config.NamedRouter, model string, vision bool) string {
	if nr.Capability != config.CapChat {
		return fmt.Sprintf("polypus: router %q does not support chat", model)
	}
	if vision {
		return fmt.Sprintf("polypus: router %q does not support vision (v1 chat-only)", model)
	}
	return ""
}

// namedRouterDispatch is the route-type branch for named routers (pure).
type namedRouterDispatch int

const (
	dispatchUnknown namedRouterDispatch = iota
	dispatchPassthrough
	dispatchSwitchyard
)

func classifyNamedRouterRoute(routeType string) namedRouterDispatch {
	switch routeType {
	case config.RoutePassthrough:
		return dispatchPassthrough
	case config.RouteStageRouter, config.RouteLLMClassifier:
		return dispatchSwitchyard
	default:
		return dispatchUnknown
	}
}
