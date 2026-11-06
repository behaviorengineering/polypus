package gatewayconfig

import (
	"sort"
	"strings"
)

const (
	// cloudflareJEVSlug is the Workers AI jev model a Cloudflare backend serves.
	cloudflareJEVSlug  = "typesafe/jev"
	openRouterDeciderV = "perplexity/pplx-decider-v1.2"
)

// SystemOneSmokeTargets builds SystemOne probes from backends with systemone capability.
// smoke.skip leaves a backend out and smoke.systemone names its probe model.
// Otherwise Cloudflare uses typesafe/jev, OpenRouter one pplx-decider slug (v1.2
// preferred) and any other backend its first systemone_allow model.
func SystemOneSmokeTargets(path string) ([]ChatSmokeTarget, error) {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(cfg.Backends))
	for id := range cfg.Backends {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []ChatSmokeTarget
	for _, id := range ids {
		b := cfg.Backends[id]
		if b.Smoke.Skip || !backendHasCapability(b, "systemone") {
			continue
		}
		out = append(out, systemOneTargetsForBackend(id, b)...)
	}
	return out, nil
}

func systemOneTargetsForBackend(backendID string, b backendYAML) []ChatSmokeTarget {
	slug := strings.TrimSpace(b.Smoke.SystemOne)
	if slug == "" {
		switch shapeOf(b) {
		case shapeOpenRouter:
			slug = pickOpenRouterDecider(b.Models.SystemOneAllow)
		case shapeCloudflare:
			// Every Cloudflare backend serves jev, listed in systemone_allow or not.
			slug = cloudflareJEVSlug
		case shapeGemini, shapeDeepInfra, shapeGeneric:
			slug = firstNonEmpty(b.Models.SystemOneAllow)
		}
	}
	if slug == "" {
		return nil
	}
	return []ChatSmokeTarget{{
		BackendID: backendID,
		Model:     backendID + "/" + slug,
		SecretEnv: backendSmokeSecrets(b),
	}}
}

func pickOpenRouterDecider(allow []string) string {
	var first string
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if s == "" || !strings.HasPrefix(s, "perplexity/pplx-decider") {
			continue
		}
		if s == openRouterDeciderV {
			return s
		}
		if first == "" {
			first = s
		}
	}
	return first
}

func firstNonEmpty(vals []string) string {
	for _, s := range vals {
		s = strings.TrimSpace(s)
		if s != "" {
			return s
		}
	}
	return ""
}
