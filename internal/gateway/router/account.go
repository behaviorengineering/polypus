package router

import (
	"context"
	"fmt"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

// ProviderSwitchyard is the Bifrost provider id for composed Switchyard hops.
// Must stay equal to upstream.NameSwitchyard (see gateway TestSwitchyardNameAligned).
const ProviderSwitchyard = "switchyard"

// Account implements schemas.Account for Polypus OpenAI-compatible backends,
// including Cloudflare chat/embed (Workers AI OpenAI-compat URL) and a synthetic
// Switchyard provider. CF TTS/STT are AllowedRequests on Bifrost; a PreLLMHook
// plugin short-circuits them onto extension /ai/run (no /ai/v1/audio/*).
type Account struct {
	backends map[schemas.ModelProvider]config.BackendDef
	timeouts config.Timeouts
}

// NewAccount returns a Bifrost account for leaf backends plus optional Switchyard.
func NewAccount(cfg config.RouterConfig) *Account {
	backends := make(map[schemas.ModelProvider]config.BackendDef, len(cfg.Backends)+1)
	for id, b := range cfg.Backends {
		backends[schemas.ModelProvider(id)] = b
	}
	if shouldRegisterSwitchyard(cfg) {
		backends[schemas.ModelProvider(ProviderSwitchyard)] = config.BackendDef{
			ID:           ProviderSwitchyard,
			BaseURL:      cfg.EffectiveSwitchyardBaseURL(),
			Capabilities: []config.Capability{config.CapChat},
		}
	}
	return &Account{backends: backends, timeouts: cfg.Timeouts}
}

func shouldRegisterSwitchyard(cfg config.RouterConfig) bool {
	if cfg.HasComposedRouters() {
		return true
	}
	return config.SwitchyardEnabled()
}

func (a *Account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, 0, len(a.backends))
	for id := range a.backends {
		out = append(out, id)
	}
	return out, nil
}

func (a *Account) GetKeysForProvider(_ context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	b, ok := a.backends[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}
	secret := "local"
	if b.Remote {
		token, err := b.Auth.ResolveBearerToken()
		if err != nil {
			return nil, err
		}
		secret = token
	}
	return []schemas.Key{{
		Value:  *schemas.NewSecretVar(secret),
		Models: []string{"*"},
		Weight: 1.0,
	}}, nil
}

func (a *Account) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	b, ok := a.backends[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}
	if provider == schemas.OpenRouter {
		return openRouterProviderConfig(b, a.timeouts), nil
	}
	baseURL := openAIBaseURL(b.BaseURL)
	baseType := schemas.OpenAI
	if b.IsGeminiExtension() {
		baseType = schemas.Gemini
		if strings.TrimSpace(b.BaseURL) == "" {
			baseURL = ""
		} else {
			baseURL = strings.TrimRight(strings.TrimSpace(b.BaseURL), "/")
		}
	}
	if b.IsAnthropicExtension() {
		baseType = schemas.Anthropic
		baseURL = anthropicBaseURL(b.BaseURL)
	}
	return &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        baseURL,
			DefaultRequestTimeoutInSeconds: a.timeouts.ProviderSeconds(),
			AllowPrivateNetwork:            true,
			ExtraHeaders:                   secretExtraHeaders(b.ExtraHeaders),
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: 8,
			BufferSize:  32,
		},
		SendBackRawResponse: true,
		CustomProviderConfig: &schemas.CustomProviderConfig{
			BaseProviderType: baseType,
			AllowedRequests:  allowedRequestsForBackend(b),
		},
	}, nil
}

// anthropicBaseURL returns the origin Bifrost's Anthropic provider expects (it appends
// /v1/messages). Empty keeps Bifrost's default, https://api.anthropic.com.
func anthropicBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimSuffix(base, "/v1")
}

func openRouterProviderConfig(b config.BackendDef, timeouts config.Timeouts) *schemas.ProviderConfig {
	return &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        openAIBaseURL(b.BaseURL),
			DefaultRequestTimeoutInSeconds: timeouts.ProviderSeconds(),
			AllowPrivateNetwork:            true,
			ExtraHeaders:                   secretExtraHeaders(b.ExtraHeaders),
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: 8,
			BufferSize:  32,
		},
		SendBackRawResponse: true,
	}
}

func secretExtraHeaders(h map[string]string) map[string]schemas.SecretVar {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]schemas.SecretVar, len(h))
	for k, v := range h {
		out[k] = *schemas.NewSecretVar(v)
	}
	return out
}

func allowedRequestsForBackend(b config.BackendDef) *schemas.AllowedRequests {
	allowed := &schemas.AllowedRequests{}
	if b.IsCloudflareExtension() {
		// Chat/embed use Workers AI OpenAI-compat. Speech/transcription are enabled so
		// Bifrost accepts the dial; cfRunSpeechPlugin short-circuits onto /ai/run.
		// Stream speech is left off (unsupported on the CF path).
		if b.HasCapability(config.CapChat) || b.HasCapability(config.CapVision) {
			allowed.ChatCompletion = true
			allowed.ChatCompletionStream = true
		}
		if b.HasCapability(config.CapEmbed) {
			allowed.Embedding = true
		}
		if b.HasCapability(config.CapTTS) {
			allowed.Speech = true
		}
		if b.HasCapability(config.CapSTT) {
			allowed.Transcription = true
		}
		return allowed
	}
	if b.HasCapability(config.CapTTS) {
		allowed.Speech = true
		allowed.SpeechStream = true
	}
	if b.HasCapability(config.CapSTT) {
		allowed.Transcription = true
		allowed.TranscriptionStream = true
	}
	if b.HasCapability(config.CapChat) || b.HasCapability(config.CapVision) {
		allowed.ChatCompletion = true
		allowed.ChatCompletionStream = true
	}
	if b.HasCapability(config.CapEmbed) {
		allowed.Embedding = true
	}
	return allowed
}
