package gateway

import (
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

func parseNamedRouterModel(model string) (name string, ok bool) {
	model = strings.TrimSpace(model)
	if !strings.HasPrefix(model, config.RouterIDPrefix) {
		return "", false
	}
	name = strings.TrimPrefix(model, config.RouterIDPrefix)
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func lookupRouter(cfg config.RouterConfig, name string) (config.NamedRouter, bool) {
	return cfg.LookupNamedRouter(name)
}
