package router

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/behaviorengineering/polypus/internal/config"
)

// Account implements schemas.Account for multi-backend local speech routing.
type Account struct {
	backends map[schemas.ModelProvider]config.BackendDef
}

// NewAccount returns a Bifrost account for validated local backends.
func NewAccount(cfg config.RouterConfig) *Account {
	backends := make(map[schemas.ModelProvider]config.BackendDef, len(cfg.Backends))
	for id, b := range cfg.Backends {
		backends[schemas.ModelProvider(id)] = b
	}
	return &Account{backends: backends}
}

func (a *Account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, 0, len(a.backends))
	for id := range a.backends {
		out = append(out, id)
	}
	return out, nil
}

func (a *Account) GetKeysForProvider(_ context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	if _, ok := a.backends[provider]; !ok {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}
	return []schemas.Key{{
		Value:  *schemas.NewSecretVar("local"),
		Models: []string{"*"},
		Weight: 1.0,
	}}, nil
}

func (a *Account) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	b, ok := a.backends[provider]
	if !ok {
		return nil, fmt.Errorf("provider %s not supported", provider)
	}
	allowed := &schemas.AllowedRequests{}
	if b.HasCapability(config.CapTTS) {
		allowed.Speech = true
		allowed.SpeechStream = true
	}
	if b.HasCapability(config.CapSTT) {
		allowed.Transcription = true
		allowed.TranscriptionStream = true
	}
	return &schemas.ProviderConfig{
		NetworkConfig: schemas.NetworkConfig{
			BaseURL:                        OpenAIBaseURL(b.BaseURL),
			DefaultRequestTimeoutInSeconds: 300,
			AllowPrivateNetwork:            true,
		},
		ConcurrencyAndBufferSize: schemas.ConcurrencyAndBufferSize{
			Concurrency: 8,
			BufferSize:  32,
		},
		CustomProviderConfig: &schemas.CustomProviderConfig{
			BaseProviderType: schemas.OpenAI,
			AllowedRequests:  allowed,
		},
	}, nil
}
