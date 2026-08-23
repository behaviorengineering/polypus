package router

import (
	"fmt"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/extension/cloudflare"
	"github.com/maximhq/bifrost/core/schemas"
)

// Registry resolves model + capability to a Bifrost provider and downstream model id.
type Registry struct {
	cfg config.RouterConfig
}

// NewRegistry validates backend URLs and builds the routing table.
func NewRegistry(cfg config.RouterConfig) (*Registry, error) {
	for id, b := range cfg.Backends {
		if err := ValidateBackend(b, cfg.Policy); err != nil {
			return nil, fmt.Errorf("backends.%s: %w", id, err)
		}
		if b.IsCloudflareExtension() {
			if _, err := cloudflare.AIBaseURL(b.BaseURL); err != nil {
				return nil, fmt.Errorf("backends.%s: %w", id, err)
			}
		}
	}
	return &Registry{cfg: cfg}, nil
}

// Config returns the router configuration.
func (r *Registry) Config() config.RouterConfig {
	return r.cfg
}

// ProxyBackendURL is the fallback proxy target for voices and other paths.
func (r *Registry) ProxyBackendURL() string {
	return r.cfg.ProxyBackendURL()
}

// ResolveEmbed picks backend and model for text embeddings.
func (r *Registry) ResolveEmbed(model string) (string, string, error) {
	return r.resolveCapability(config.CapEmbed, r.cfg.DefaultEmbedBackend, model)
}

// ResolveChat picks backend and model for text chat completions.
func (r *Registry) ResolveChat(model string) (string, string, error) {
	return r.resolveCapability(config.CapChat, r.cfg.DefaultChatBackend, model)
}

// ResolveVision picks backend and model for multimodal chat completions.
func (r *Registry) ResolveVision(model string) (string, string, error) {
	defaultBackend := r.cfg.DefaultVisionBackend
	if defaultBackend == "" {
		defaultBackend = r.cfg.DefaultChatBackend
	}
	return r.resolveCapability(config.CapVision, defaultBackend, model)
}

// ResolveTTS picks provider and model for speech synthesis.
func (r *Registry) ResolveTTS(model string) (schemas.ModelProvider, string, error) {
	id, downstream, err := r.resolveCapability(config.CapTTS, r.cfg.DefaultTTSBackend, model)
	if err != nil {
		return "", "", err
	}
	return schemas.ModelProvider(id), downstream, nil
}

// ResolveSTT picks provider and model for transcription.
func (r *Registry) ResolveSTT(model string) (schemas.ModelProvider, string, error) {
	id, downstream, err := r.resolveCapability(config.CapSTT, r.cfg.DefaultSTTBackend, model)
	if err != nil {
		return "", "", err
	}
	return schemas.ModelProvider(id), downstream, nil
}

func (r *Registry) resolveCapability(cap config.Capability, defaultBackend, model string) (string, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		if cap == config.CapTTS || cap == config.CapSTT {
			if defaultBackend == "" {
				return "", "", fmt.Errorf("no default backend for %s", cap)
			}
			b, ok := r.cfg.Backends[defaultBackend]
			if !ok {
				return "", "", fmt.Errorf("default backend %q not found", defaultBackend)
			}
			if !b.HasCapability(cap) {
				return "", "", fmt.Errorf("default backend %q does not support %s", defaultBackend, cap)
			}
			return defaultBackend, "", nil
		}
		return "", "", fmt.Errorf("model required")
	}
	if i := strings.Index(model, "/"); i > 0 {
		prefix := model[:i]
		if b, ok := r.cfg.Backends[prefix]; ok {
			if !b.HasCapability(cap) {
				return "", "", fmt.Errorf("backend %q does not support %s", prefix, cap)
			}
			rest := strings.TrimSpace(model[i+1:])
			if rest == "" {
				return "", "", fmt.Errorf("model required after backend prefix")
			}
			return prefix, rest, nil
		}
	}
	if defaultBackend == "" {
		return "", "", fmt.Errorf("no default backend for %s", cap)
	}
	b, ok := r.cfg.Backends[defaultBackend]
	if !ok {
		return "", "", fmt.Errorf("default backend %q not found", defaultBackend)
	}
	if !b.HasCapability(cap) {
		return "", "", fmt.Errorf("default backend %q does not support %s", defaultBackend, cap)
	}
	return defaultBackend, model, nil
}

// BackendURL returns the base URL for a backend id.
func (r *Registry) BackendURL(id string) (string, bool) {
	b, ok := r.cfg.Backends[id]
	if !ok {
		return "", false
	}
	return b.BaseURL, true
}

// Backend returns the backend definition for an id.
func (r *Registry) Backend(id string) (config.BackendDef, bool) {
	b, ok := r.cfg.Backends[id]
	return b, ok
}
