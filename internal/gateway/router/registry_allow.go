package router

import (
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func (r *Registry) loadCfg() config.RouterConfig {
	if r == nil {
		return config.RouterConfig{}
	}
	if v := r.cfg.Load(); v != nil {
		return *v
	}
	return config.RouterConfig{}
}

func (r *Registry) storeCfg(cfg config.RouterConfig) {
	r.cfg.Store(&cfg)
}

// AllowModel appends a downstream model id to the backend allow list in memory.
func (r *Registry) AllowModel(backendID, model string) error {
	if r == nil {
		return derrors.New(derrors.CodeInternal, "router.AllowModel", "nil registry")
	}
	snap := r.loadCfg()
	b, ok := snap.Backends[backendID]
	if !ok {
		return derrors.New(derrors.CodeNotFound, "router.AllowModel", "backend not found").
			With("backend", backendID)
	}
	if b.Models == nil || !b.Models.HasAllowGate() {
		return derrors.New(derrors.CodeFailedPrecondition, "router.AllowModel", "backend has no models.allow gate").
			With("backend", backendID)
	}
	down := config.NormalizeDownstream(backendID, model)
	if down == "" {
		return derrors.New(derrors.CodeInvalid, "router.AllowModel", "empty model")
	}
	if config.ModelInAllowList(backendID, down, b.Models.Allow) {
		return nil
	}
	newBackends := make(map[string]config.BackendDef, len(snap.Backends))
	for id, def := range snap.Backends {
		newBackends[id] = def
	}
	newModels := &config.BackendModels{
		Sync:            b.Models.Sync,
		AllowConfigured: true,
		Allow:           append([]string(nil), b.Models.Allow...),
	}
	newModels.Allow = append(newModels.Allow, down)
	nb := b
	nb.Models = newModels
	newBackends[backendID] = nb
	snap.Backends = newBackends
	r.storeCfg(snap)
	return nil
}
