package gatewayconfig

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

// minimalReasoning is the reasoning effort smoke sends to models that think by default.
const minimalReasoning = "minimal"

// cloudflareChatSlug is the cheap Workers AI chat model smoke prefers when a
// Cloudflare backend allows it.
const cloudflareChatSlug = "@cf/ibm-granite/granite-4.0-h-micro"

// providerShape is the upstream a backend talks to, read from its extension or its
// base_url host, never from its id, so any deployer's backend names work.
type providerShape int

const (
	shapeGeneric providerShape = iota
	shapeCloudflare
	shapeGemini
	shapeOpenRouter
	shapeDeepInfra
)

func shapeOf(b backendYAML) providerShape {
	switch strings.ToLower(strings.TrimSpace(b.Extension)) {
	case "cloudflare":
		return shapeCloudflare
	case "gemini":
		return shapeGemini
	}
	switch host := baseURLHost(b.BaseURL); {
	case hostIs(host, "api.cloudflare.com"):
		return shapeCloudflare
	case hostIs(host, "openrouter.ai"):
		return shapeOpenRouter
	case hostIs(host, "deepinfra.com"):
		return shapeDeepInfra
	default:
		return shapeGeneric
	}
}

// baseURLHost is the lower-case host of a base_url; ${VAR} placeholders in the
// path do not stop the parse.
func baseURLHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// hostIs reports whether host is domain or a subdomain of it.
func hostIs(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// ChatSmokeTarget is one post-deploy chat probe derived from gateway YAML.
type ChatSmokeTarget struct {
	BackendID       string
	Model           string
	SecretEnv       []string
	ReasoningEffort string // when set, smoke sends reasoning.effort (e.g. minimal for Gemini).
}

// ChatSmokeTargets builds chat smoke probes from backends with chat capability.
// A backend's smoke block decides first: smoke.skip leaves it out and smoke.chat
// names its probe models. Otherwise the provider shape picks from models.allow:
// OpenRouter yields at most one mistralai/ and one google/gemini slug, DeepInfra at
// most one deepseek-ai/ slug plus Gemma 4 26B and 31B (not Ultra or turbo, not
// Gemma 3), and any other backend its first allowed model.
func ChatSmokeTargets(path string) ([]ChatSmokeTarget, error) {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	var out []ChatSmokeTarget
	for _, id := range slices.Sorted(maps.Keys(cfg.Backends)) {
		b := cfg.Backends[id]
		if b.Smoke.Skip || !backendHasCapability(b, "chat") {
			continue
		}
		secrets := backendSmokeSecrets(b)
		for _, t := range chatTargetsForBackend(id, b) {
			t.SecretEnv = secrets
			out = append(out, t)
		}
	}
	return out, nil
}

func backendHasCapability(b backendYAML, name string) bool {
	for _, c := range b.Capabilities {
		if strings.EqualFold(strings.TrimSpace(c), name) {
			return true
		}
	}
	return false
}

// backendSmokeSecrets are the secrets a backend's smoke probe needs: the same
// names deploy exports for it.
func backendSmokeSecrets(b backendYAML) []string {
	return dedupeStrings(backendSecretNames(b))
}

func chatTargetsForBackend(backendID string, b backendYAML) []ChatSmokeTarget {
	shape := shapeOf(b)
	if explicit := dedupeStrings(b.Smoke.Chat); len(explicit) > 0 {
		out := make([]ChatSmokeTarget, 0, len(explicit))
		for _, slug := range explicit {
			out = append(out, chatTarget(backendID, shape, slug))
		}
		return out
	}
	allow := b.Models.Allow
	if len(allow) == 0 {
		return nil
	}
	var slugs []string
	switch shape {
	case shapeCloudflare:
		slugs = []string{pickCFChatSlug(allow)}
	case shapeOpenRouter:
		slugs = openRouterChatSlugs(allow)
	case shapeDeepInfra:
		slugs = deepInfraChatSlugs(allow)
	case shapeGemini, shapeGeneric:
		slugs = []string{firstNonEmpty(allow)}
	}
	var out []ChatSmokeTarget
	for _, slug := range slugs {
		if slug != "" {
			out = append(out, chatTarget(backendID, shape, slug))
		}
	}
	return out
}

// chatTarget is the probe for slug on backendID, with minimal reasoning for the
// models that think by default (Gemini, and Gemma 4 on DeepInfra).
func chatTarget(backendID string, shape providerShape, slug string) ChatSmokeTarget {
	t := ChatSmokeTarget{BackendID: backendID, Model: backendID + "/" + slug}
	if shape == shapeGemini || (shape == shapeDeepInfra && isDeepInfraGemma4(strings.ToLower(slug))) {
		t.ReasoningEffort = minimalReasoning
	}
	return t
}

func pickCFChatSlug(allow []string) string {
	for _, s := range allow {
		if strings.TrimSpace(s) == cloudflareChatSlug {
			return cloudflareChatSlug
		}
	}
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "typesafe/") {
			continue
		}
		if strings.Contains(s, "deepgram") || strings.Contains(s, "bge-") || strings.Contains(s, "whisper") {
			continue
		}
		if strings.HasPrefix(s, "@cf/") {
			return s
		}
	}
	return ""
}

func openRouterChatSlugs(allow []string) []string {
	var mistral, gemini string
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if mistral == "" && strings.HasPrefix(s, "mistralai/") {
			mistral = s
		}
		if gemini == "" && strings.HasPrefix(s, "google/gemini") {
			gemini = s
		}
	}
	return []string{mistral, gemini}
}

func deepInfraChatSlugs(allow []string) []string {
	var deepseek, gemma26, gemma31 string
	for _, s := range allow {
		s = strings.TrimSpace(s)
		low := strings.ToLower(s)
		if deepseek == "" && strings.HasPrefix(low, "deepseek-ai/") {
			deepseek = s
		}
		if gemma26 == "" && strings.Contains(low, "gemma-4-26b") {
			gemma26 = s
		}
		if gemma31 == "" && isDeepInfraGemma431B(low) {
			gemma31 = s
		}
	}
	return []string{deepseek, gemma26, gemma31}
}

// isDeepInfraGemma4 reports whether a lower-case DeepInfra slug is a Gemma 4 model.
func isDeepInfraGemma4(low string) bool {
	return strings.Contains(low, "gemma-4-")
}

func isDeepInfraGemma431B(low string) bool {
	if !strings.Contains(low, "gemma-4-31b-it") {
		return false
	}
	if strings.Contains(low, "ultra") || strings.Contains(low, "turbo") {
		return false
	}
	return true
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// SecretsReady reports whether every required env name has a non-empty value in present.
func SecretsReady(target ChatSmokeTarget, present map[string]bool) bool {
	for _, env := range target.SecretEnv {
		if !present[env] {
			return false
		}
	}
	return true
}

// MissingSecrets lists env names that are required but not present.
func MissingSecrets(target ChatSmokeTarget, present map[string]bool) []string {
	var miss []string
	for _, env := range target.SecretEnv {
		if !present[env] {
			miss = append(miss, env)
		}
	}
	return miss
}
