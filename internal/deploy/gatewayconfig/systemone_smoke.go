package gatewayconfig

import (
	"sort"
	"strings"
)

const (
	homelabJEVSlug     = "typesafe/jev"
	openRouterDeciderV = "perplexity/pplx-decider-v1.2"
)

// SystemOneSmokeTargets builds SystemOne probes from backends with systemone capability.
// Cloudflare uses typesafe/jev. OpenRouter yields one pplx-decider slug (prefer v1.2).
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
		if !backendHasCapability(b, "systemone") {
			continue
		}
		out = append(out, systemOneTargetsForBackend(id, b)...)
	}
	return out, nil
}

func systemOneTargetsForBackend(backendID string, b backendYAML) []ChatSmokeTarget {
	secrets := backendSmokeSecrets(backendID, b)
	ext := strings.ToLower(strings.TrimSpace(b.Extension))
	switch {
	case backendID == "openrouter":
		slug := pickOpenRouterDecider(b.Models.SystemOneAllow)
		if slug == "" {
			return nil
		}
		return []ChatSmokeTarget{{
			BackendID: backendID,
			Model:     backendID + "/" + slug,
			SecretEnv: secrets,
		}}
	case backendID == cloudflareLocalBackend || ext == "cloudflare":
		slug := pickCFSystemOneSlug(b.Models.SystemOneAllow)
		if slug == "" {
			slug = homelabJEVSlug
		}
		return []ChatSmokeTarget{{
			BackendID: backendID,
			Model:     backendID + "/" + slug,
			SecretEnv: secrets,
		}}
	default:
		slug := firstNonEmpty(b.Models.SystemOneAllow)
		if slug == "" {
			return nil
		}
		return []ChatSmokeTarget{{
			BackendID: backendID,
			Model:     backendID + "/" + slug,
			SecretEnv: secrets,
		}}
	}
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

func pickCFSystemOneSlug(allow []string) string {
	for _, s := range allow {
		if strings.TrimSpace(s) == homelabJEVSlug {
			return homelabJEVSlug
		}
	}
	return ""
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

// ValidateSystemOneSmokeTargets ensures models are non-empty when targets exist.
func ValidateSystemOneSmokeTargets(targets []ChatSmokeTarget) error {
	return ValidateChatSmokeTargets(targets)
}
