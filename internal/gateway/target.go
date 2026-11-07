package gateway

import (
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// backendTarget is the backend one request goes to: where it lives, its definition
// and the credential the gateway sends it.
type backendTarget struct {
	id         string
	downstream string
	url        string
	auth       string
	backend    config.BackendDef
}

// lookupBackend returns a resolved backend's definition and URL. op names the
// handler in the error. A resolved id the live config no longer holds (an allow or
// reload swapped it out) is local state, so it is CodeNotReady, not a failed hop.
func lookupBackend(reg *router.Registry, op, backendID string) (config.BackendDef, string, error) {
	url, ok := reg.BackendURL(backendID)
	if !ok {
		return config.BackendDef{}, "", errBackendNotFound(op, backendID)
	}
	b, ok := reg.Backend(backendID)
	if !ok {
		return config.BackendDef{}, "", errBackendNotFound(op, backendID)
	}
	return b, url, nil
}

// errBackendNotFound is a backend id the live config does not hold.
func errBackendNotFound(op, backendID string) error {
	return derrors.New(derrors.CodeNotReady, op, msgBackendNotFound).With("backend", backendID)
}

// resolveTarget is lookupBackend plus the backend's credential.
func resolveTarget(reg *router.Registry, op, backendID, downstream string) (backendTarget, error) {
	b, url, err := lookupBackend(reg, op, backendID)
	if err != nil {
		return backendTarget{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "gateway.resolveTarget", "lookup backend")
	}
	auth, err := bearerAuthHeader(b)
	if err != nil {
		return backendTarget{}, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeNotReady), "gateway.resolveTarget", "backend credential").
			With("backend", b.ID)
	}
	return backendTarget{id: backendID, downstream: downstream, url: url, auth: auth, backend: b}, nil
}
