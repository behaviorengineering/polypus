package router

import (
	"context"
	"fmt"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

// ProviderSwitchyard is the Bifrost provider id for composed Switchyard hops.
// Must stay equal to upstream.NameSwitchyard (see gateway TestSwitchyardNameAligned).
const ProviderSwitchyard = "switchyard"

// Account implements schemas.Account for Polypus OpenAI-compatible backends,
// including Cloudflare chat/embed (Workers AI OpenAI-compat URL) and a synthetic
// Switchyard provider. CF TTS/STT stay on the extension (/run), not Bifrost speech.
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
	return &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        openAIBaseURL(b.BaseURL),
			DefaultRequestTimeoutInSeconds: a.timeouts.ProviderSeconds(),
			AllowPrivateNetwork:            true,
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: 8,
			BufferSize:  32,
		},
		CustomProviderConfig: &schemas.CustomProviderConfig{
			BaseProviderType: schemas.OpenAI,
			AllowedRequests:  allowedRequestsForBackend(b),
		},
	}, nil
}

func allowedRequestsForBackend(b config.BackendDef) *schemas.AllowedRequests {
	allowed := &schemas.AllowedRequests{}
	if b.IsCloudflareExtension() {
		// Bifrost fronts CF OpenAI-compat chat/embed only; /run speech stays on extension.
		if b.HasCapability(config.CapChat) || b.HasCapability(config.CapVision) {
			allowed.ChatCompletion = true
			allowed.ChatCompletionStream = true
		}
		if b.HasCapability(config.CapEmbed) {
			allowed.Embedding = true
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
