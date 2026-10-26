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
// It compare-and-swaps the config snapshot, so two concurrent calls both land.
func (r *Registry) AllowModel(backendID, model string) error {
	if r == nil {
		return derrors.New(derrors.CodeInternal, "router.AllowModel", "nil registry")
	}
	for {
		old := r.cfg.Load()
		var snap config.RouterConfig
		if old != nil {
			snap = *old
		}
		next, changed, err := withAllowedModel(snap, backendID, model)
		if err != nil || !changed {
			return err
		}
		if r.cfg.CompareAndSwap(old, &next) {
			return nil
		}
	}
}

// withAllowedModel returns snap with model added to backendID's allow list. changed
// is false when the model was already allowed. snap itself is not modified.
func withAllowedModel(snap config.RouterConfig, backendID, model string) (config.RouterConfig, bool, error) {
	b, ok := snap.Backends[backendID]
	if !ok {
		return snap, false, derrors.New(derrors.CodeNotFound, "router.AllowModel", "backend not found").
			With("backend", backendID)
	}
	if b.Models == nil || !b.Models.HasAllowGate() {
		return snap, false, derrors.New(derrors.CodeFailedPrecondition, "router.AllowModel", "backend has no models.allow gate").
			With("backend", backendID)
	}
	down := config.NormalizeDownstream(backendID, model)
	if down == "" {
		return snap, false, derrors.New(derrors.CodeInvalid, "router.AllowModel", "empty model")
	}
	if config.ModelInAllowList(backendID, down, b.Models.Allow) {
		return snap, false, nil
	}
	newBackends := make(map[string]config.BackendDef, len(snap.Backends))
	for id, def := range snap.Backends {
		newBackends[id] = def
	}
	nb := b
	nb.Models = &config.BackendModels{
		Sync:            b.Models.Sync,
		AllowConfigured: true,
		Allow:           append(append([]string(nil), b.Models.Allow...), down),
	}
	newBackends[backendID] = nb
	snap.Backends = newBackends
	return snap, true, nil
}
