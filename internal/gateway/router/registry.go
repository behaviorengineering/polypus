package router

import (
	"fmt"
	"strings"
	"sync/atomic"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/maximhq/bifrost/core/schemas"
)

// Registry resolves model + capability to a Bifrost provider and downstream model id.
type Registry struct {
	cfg atomic.Pointer[config.RouterConfig]
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
	reg := &Registry{}
	reg.storeCfg(cfg)
	return reg, nil
}

// Config returns the router configuration.
func (r *Registry) Config() config.RouterConfig {
	return r.loadCfg()
}

// ProxyBackendURL is the fallback proxy target for voices and other paths.
func (r *Registry) ProxyBackendURL() string {
	return r.loadCfg().ProxyBackendURL()
}

// ResolveEmbed picks backend and model for text embeddings.
func (r *Registry) ResolveEmbed(model string) (string, string, error) {
	cfg := r.loadCfg()
	return resolveCapability(cfg, config.CapEmbed, cfg.EffectiveEmbedBackend(), model)
}

// ResolveChat picks backend and model for text chat completions.
func (r *Registry) ResolveChat(model string) (string, string, error) {
	cfg := r.loadCfg()
	return resolveCapability(cfg, config.CapChat, cfg.EffectiveChatBackend(), model)
}

// ResolveVision picks backend and model for multimodal chat completions.
func (r *Registry) ResolveVision(model string) (string, string, error) {
	cfg := r.loadCfg()
	defaultBackend := cfg.EffectiveVisionBackend()
	if defaultBackend == "" {
		defaultBackend = cfg.EffectiveChatBackend()
	}
	return resolveCapability(cfg, config.CapVision, defaultBackend, model)
}

// ResolveTTS picks provider and model for speech synthesis.
func (r *Registry) ResolveTTS(model string) (schemas.ModelProvider, string, error) {
	cfg := r.loadCfg()
	id, downstream, err := resolveCapability(cfg, config.CapTTS, cfg.EffectiveTTSBackend(), model)
	if err != nil {
		return "", "", err
	}
	return schemas.ModelProvider(id), downstream, nil
}

// ResolveSTT picks provider and model for transcription.
func (r *Registry) ResolveSTT(model string) (schemas.ModelProvider, string, error) {
	cfg := r.loadCfg()
	id, downstream, err := resolveCapability(cfg, config.CapSTT, cfg.EffectiveSTTBackend(), model)
	if err != nil {
		return "", "", err
	}
	return schemas.ModelProvider(id), downstream, nil
}

// DefaultSystemOneModel is used when the client omits model on POST /v1/systemone.
const DefaultSystemOneModel = "typesafe/jev"

// ResolveSystemOne picks backend and model for TypeSafe / Decider systemone evaluation.
func (r *Registry) ResolveSystemOne(model string) (string, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultSystemOneModel
	}
	cfg := r.loadCfg()
	return resolveCapability(cfg, config.CapSystemOne, cfg.EffectiveSystemOneBackend(), model)
}

// ResolveBatch picks backend and downstream model for OpenAI Batch (Cloudflare extension).
func (r *Registry) ResolveBatch(model string) (string, string, error) {
	cfg := r.loadCfg()
	return resolveCapability(cfg, config.CapBatch, cfg.EffectiveBatchBackend(), strings.TrimSpace(model))
}

// resolveCapability picks the backend and downstream model for cap from one config
// snapshot. A "backend/model" prefix selects that backend; otherwise the default.
// Errors are typed: a request the gateway cannot route is CodeInvalid, and a
// default that the config does not provide is CodeNotReady.
func resolveCapability(cfg config.RouterConfig, cap config.Capability, defaultBackend, model string) (string, string, error) {
	const op = "router.resolve"
	model = strings.TrimSpace(model)
	if model == "" {
		if cap != config.CapTTS && cap != config.CapSTT {
			return "", "", derrors.New(derrors.CodeInvalid, op, "model required").With("capability", string(cap))
		}
		// Speech may omit the model: the default backend picks its own.
		if err := checkDefaultBackend(cfg, cap, defaultBackend); err != nil {
			return "", "", err
		}
		return defaultBackend, "", nil
	}
	if i := strings.Index(model, "/"); i > 0 {
		prefix := model[:i]
		if b, ok := cfg.Backends[prefix]; ok {
			if !b.HasCapability(cap) {
				return "", "", derrors.New(derrors.CodeInvalid, op, fmt.Sprintf("backend %q does not support %s", prefix, cap)).
					With("backend", prefix)
			}
			rest := strings.TrimSpace(model[i+1:])
			if rest == "" {
				return "", "", derrors.New(derrors.CodeInvalid, op, "model required after backend prefix").With("backend", prefix)
			}
			return prefix, rest, nil
		}
	}
	if err := checkDefaultBackend(cfg, cap, defaultBackend); err != nil {
		return "", "", err
	}
	return defaultBackend, model, nil
}

// checkDefaultBackend reports whether defaultBackend exists and serves cap.
func checkDefaultBackend(cfg config.RouterConfig, cap config.Capability, defaultBackend string) error {
	const op = "router.resolve"
	if defaultBackend == "" {
		return derrors.New(derrors.CodeInvalid, op, fmt.Sprintf("no default backend for %s; prefix the model with a backend id", cap))
	}
	b, ok := cfg.Backends[defaultBackend]
	if !ok {
		return derrors.New(derrors.CodeNotReady, op, fmt.Sprintf("default backend %q not found", defaultBackend))
	}
	if !b.HasCapability(cap) {
		return derrors.New(derrors.CodeNotReady, op, fmt.Sprintf("default backend %q does not support %s", defaultBackend, cap))
	}
	return nil
}

// BackendURL returns the base URL for a backend id.
func (r *Registry) BackendURL(id string) (string, bool) {
	b, ok := r.loadCfg().Backends[id]
	if !ok {
		return "", false
	}
	return b.BaseURL, true
}

// Backend returns the backend definition for an id.
func (r *Registry) Backend(id string) (config.BackendDef, bool) {
	b, ok := r.loadCfg().Backends[id]
	return b, ok
}
