package router

import (
	"context"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/maximhq/bifrost/core/schemas"
)

// ProviderSwitchyard is the Bifrost provider id for composed Switchyard hops.
// Must stay equal to upstream.NameSwitchyard (see gateway TestSwitchyardNameAligned).
const ProviderSwitchyard = "switchyard"

// account implements schemas.Account for Polypus OpenAI-compatible backends,
// including Cloudflare chat/embed (Workers AI OpenAI-compat URL) and a synthetic
// Switchyard provider. CF TTS/STT are AllowedRequests on Bifrost; a PreLLMHook
// plugin short-circuits them onto extension /ai/run (no /ai/v1/audio/*).
type account struct {
	backends map[schemas.ModelProvider]config.BackendDef
	timeouts config.Timeouts
}

// newAccount returns a Bifrost account for leaf backends plus optional Switchyard.
func newAccount(cfg config.RouterConfig) *account {
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
	return &account{backends: backends, timeouts: cfg.Timeouts}
}

func shouldRegisterSwitchyard(cfg config.RouterConfig) bool {
	if cfg.HasComposedRouters() {
		return true
	}
	return config.SwitchyardEnabled()
}

// GetConfiguredProviders implements schemas.Account: one provider per registered backend.
func (a *account) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	out := make([]schemas.ModelProvider, 0, len(a.backends))
	for id := range a.backends {
		out = append(out, id)
	}
	return out, nil
}

// GetKeysForProvider implements schemas.Account: the backend's resolved credential.
func (a *account) GetKeysForProvider(_ context.Context, provider schemas.ModelProvider) ([]schemas.Key, error) {
	b, ok := a.backends[provider]
	if !ok {
		return nil, unsupportedProvider("router.account.GetKeysForProvider", provider)
	}
	secret := "local"
	if b.Remote {
		token, err := b.Auth.ResolveBearerToken()
		if err != nil {
			// A missing credential describes the config, not the backend's health,
			// so it must not read as a provider failure that trips the breaker.
			return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeUnauthorized), "router.account.GetKeysForProvider", "resolve credential").
				With(fieldBackend, string(provider))
		}
		secret = token
	}
	return []schemas.Key{{
		Value:  *schemas.NewSecretVar(secret),
		Models: []string{"*"},
		Weight: 1.0,
	}}, nil
}

// GetConfigForProvider implements schemas.Account: the backend's base URL, headers and limits.
func (a *account) GetConfigForProvider(provider schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	b, ok := a.backends[provider]
	if !ok {
		return nil, unsupportedProvider("router.account.GetConfigForProvider", provider)
	}
	if provider == schemas.OpenRouter {
		return openRouterProviderConfig(b, a.timeouts), nil
	}
	baseURL := openAIBaseURL(b.BaseURL)
	baseType := schemas.OpenAI
	if b.IsGeminiExtension() {
		baseType = schemas.Gemini
		// Empty keeps Bifrost's default Gemini host.
		baseURL = strings.TrimRight(strings.TrimSpace(b.BaseURL), "/")
	}
	if b.IsAnthropicExtension() {
		baseType = schemas.Anthropic
		baseURL = anthropicBaseURL(b.BaseURL)
	}
	if b.IsTypesafeExtension() {
		// Bifrost's Typesafe provider serves SystemOne as its decision operation; it
		// appends /v1/systemone, so the base is the origin. Empty keeps its default host.
		baseType = schemas.Typesafe
		baseURL = anthropicBaseURL(b.BaseURL)
	}
	return &schemas.ProviderConfig{
		NetworkConfig:            networkConfig(baseURL, b, a.timeouts),
		ConcurrencyAndBufferSize: providerPool,
		SendBackRawResponse:      true,
		CustomProviderConfig: &schemas.CustomProviderConfig{
			BaseProviderType: baseType,
			AllowedRequests:  allowedRequestsForBackend(b),
		},
	}, nil
}

// unsupportedProvider is the error for a provider no backend registers.
func unsupportedProvider(op string, provider schemas.ModelProvider) error {
	return derrors.New(derrors.CodeNotFound, op, "provider not supported").
		With(fieldProvider, string(provider))
}

// anthropicBaseURL returns the origin Bifrost's Anthropic provider expects (it appends
// /v1/messages); the Typesafe provider wants the same (it appends /v1/systemone).
// Empty keeps the provider's default host.
func anthropicBaseURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimSuffix(base, "/v1")
}

// networkConfig is how Bifrost reaches a backend at baseURL.
func networkConfig(baseURL string, b config.BackendDef, timeouts config.Timeouts) schemas.NetworkConfig {
	return schemas.NetworkConfig{
		BaseURL:                        baseURL,
		DefaultRequestTimeoutInSeconds: timeouts.ProviderSeconds(),
		AllowPrivateNetwork:            true,
		ExtraHeaders:                   secretExtraHeaders(b.ExtraHeaders),
	}
}

// providerPool is each backend's Bifrost worker pool: concurrent requests and the
// queue behind them.
var providerPool = schemas.ConcurrencyAndBufferSize{Concurrency: 8, BufferSize: 32}

func openRouterProviderConfig(b config.BackendDef, timeouts config.Timeouts) *schemas.ProviderConfig {
	return &schemas.ProviderConfig{
		NetworkConfig:            networkConfig(openAIBaseURL(b.BaseURL), b, timeouts),
		ConcurrencyAndBufferSize: providerPool,
		SendBackRawResponse:      true,
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
	chat := b.HasCapability(config.CapChat) || b.HasCapability(config.CapVision)
	tts, stt := b.HasCapability(config.CapTTS), b.HasCapability(config.CapSTT)
	allowed := &schemas.AllowedRequests{
		ChatCompletion:       chat,
		ChatCompletionStream: chat,
		Embedding:            b.HasCapability(config.CapEmbed),
		Speech:               tts,
		Transcription:        stt,
		Decision:             b.HasCapability(config.CapSystemOne) && b.IsTypesafeExtension(),
	}
	if b.IsCloudflareExtension() {
		// Chat/embed use Workers AI OpenAI-compat. Speech/transcription are enabled so
		// Bifrost accepts the dial; cfRunSpeechPlugin short-circuits onto /ai/run.
		// Stream speech is left off (unsupported on the CF path).
		return allowed
	}
	allowed.SpeechStream = tts
	allowed.TranscriptionStream = stt
	if b.IsAnthropicExtension() && b.HasCapability(config.CapBatch) {
		allowed.BatchCreate = true
		allowed.BatchRetrieve = true
		allowed.BatchResults = true
		allowed.BatchCancel = true
	}
	return allowed
}
