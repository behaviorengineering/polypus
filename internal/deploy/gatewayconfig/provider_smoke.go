package gatewayconfig

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

const (
	// cloudflareLocalBackend is the Cloudflare Workers AI backend id the homelab uses.
	cloudflareLocalBackend = "cf_local"
	// minimalReasoning is the reasoning effort smoke sends to models that think by default.
	minimalReasoning = "minimal"
)

const homelabGraniteSlug = "@cf/ibm-granite/granite-4.0-h-micro"

// ChatSmokeTarget is one post-deploy chat probe derived from gateway YAML.
type ChatSmokeTarget struct {
	BackendID       string
	Model           string
	SecretEnv       []string
	ReasoningEffort string // when set, smoke sends reasoning.effort (e.g. minimal for Gemini).
}

// ChatSmokeTargets builds chat smoke probes from backends with chat capability and allow-listed models.
// OpenRouter yields at most one mistralai/ and one google/gemini slug. DeepInfra yields at most one
// deepseek-ai/ slug plus Gemma 4 26B and 31B (not Ultra/turbo, not Gemma 3). lm_studio runs only when
// POLYPUS_SMOKE_LM_STUDIO=1.
func ChatSmokeTargets(path string) ([]ChatSmokeTarget, error) {
	cfg, err := loadGatewayYAML(path)
	if err != nil {
		return nil, err
	}
	lmStudio := strings.TrimSpace(os.Getenv("POLYPUS_SMOKE_LM_STUDIO")) == "1"

	ids := make([]string, 0, len(cfg.Backends))
	for id := range cfg.Backends {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var out []ChatSmokeTarget
	for _, id := range ids {
		b := cfg.Backends[id]
		if !backendHasCapability(b, "chat") {
			continue
		}
		if id == "lm_studio" && !lmStudio {
			continue
		}
		// Homelab: Gemma on gemini_free often returns empty content under deploy smoke
		// (reasoning + low max_tokens). Probe gemini_paid for Google AI Studio instead.
		if id == "gemini_free" {
			continue
		}
		secrets := backendSmokeSecrets(id, b)
		targets := chatTargetsForBackend(id, b)
		for _, t := range targets {
			t.SecretEnv = secrets
			out = append(out, t)
		}
	}
	return out, nil
}

func backendHasCapability(b backendYAML, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range b.Capabilities {
		if strings.EqualFold(strings.TrimSpace(c), name) {
			return true
		}
	}
	return false
}

func backendSmokeSecrets(backendID string, b backendYAML) []string {
	var out []string
	if env := strings.TrimSpace(b.Auth.BearerEnv); env != "" {
		out = append(out, env)
	}
	if strings.TrimSpace(backendID) == cloudflareLocalBackend {
		out = append(out, "CF_ACCOUNT_ID")
	}
	return dedupeStrings(out)
}

func chatTargetsForBackend(backendID string, b backendYAML) []ChatSmokeTarget {
	allow := b.Models.Allow
	if len(allow) == 0 {
		return nil
	}
	ext := strings.ToLower(strings.TrimSpace(b.Extension))
	switch {
	case backendID == cloudflareLocalBackend || ext == "cloudflare":
		slug := pickCFChatSlug(allow)
		if slug == "" {
			return nil
		}
		return []ChatSmokeTarget{{
			BackendID: backendID,
			Model:     backendID + "/" + slug,
		}}
	case ext == "gemini":
		slug := strings.TrimSpace(allow[0])
		if slug == "" {
			return nil
		}
		return []ChatSmokeTarget{{
			BackendID:       backendID,
			Model:           backendID + "/" + slug,
			ReasoningEffort: minimalReasoning,
		}}
	case backendID == "openrouter":
		return openRouterChatTargets(backendID, allow)
	case backendID == "deepinfra":
		return deepInfraChatTargets(backendID, allow)
	default:
		slug := strings.TrimSpace(allow[0])
		if slug == "" {
			return nil
		}
		return []ChatSmokeTarget{{
			BackendID: backendID,
			Model:     backendID + "/" + slug,
		}}
	}
}

func pickCFChatSlug(allow []string) string {
	for _, s := range allow {
		if strings.TrimSpace(s) == homelabGraniteSlug {
			return homelabGraniteSlug
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

func openRouterChatTargets(backendID string, allow []string) []ChatSmokeTarget {
	var mistral, gemini string
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if mistral == "" && strings.HasPrefix(s, "mistralai/") {
			mistral = s
		}
		if gemini == "" && strings.HasPrefix(s, "google/gemini") {
			gemini = s
		}
	}
	var out []ChatSmokeTarget
	if mistral != "" {
		out = append(out, ChatSmokeTarget{BackendID: backendID, Model: backendID + "/" + mistral})
	}
	if gemini != "" {
		out = append(out, ChatSmokeTarget{BackendID: backendID, Model: backendID + "/" + gemini})
	}
	return out
}

func deepInfraChatTargets(backendID string, allow []string) []ChatSmokeTarget {
	var deepseek, gemma26, gemma31 string
	for _, s := range allow {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
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
	var out []ChatSmokeTarget
	if deepseek != "" {
		out = append(out, ChatSmokeTarget{BackendID: backendID, Model: backendID + "/" + deepseek})
	}
	if gemma26 != "" {
		out = append(out, ChatSmokeTarget{
			BackendID:       backendID,
			Model:           backendID + "/" + gemma26,
			ReasoningEffort: minimalReasoning,
		})
	}
	if gemma31 != "" {
		out = append(out, ChatSmokeTarget{
			BackendID:       backendID,
			Model:           backendID + "/" + gemma31,
			ReasoningEffort: minimalReasoning,
		})
	}
	return out
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
	if len(target.SecretEnv) == 0 {
		return true
	}
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

// ValidateChatSmokeTargets ensures models are non-empty when targets exist.
func ValidateChatSmokeTargets(targets []ChatSmokeTarget) error {
	for _, t := range targets {
		if strings.TrimSpace(t.Model) == "" {
			return fmt.Errorf("gatewayconfig: empty smoke model for backend %q", t.BackendID)
		}
	}
	return nil
}
