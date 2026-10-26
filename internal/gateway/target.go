package gateway

import (
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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
// handler in the error.
func (s *shared) lookupBackend(op, backendID string) (config.BackendDef, string, error) {
	reg := s.router.Registry()
	url, ok := reg.BackendURL(backendID)
	if !ok {
		return config.BackendDef{}, "", derrors.New(derrors.CodeUnavailable, op, errBackendNotFound).With("backend", backendID)
	}
	b, ok := reg.Backend(backendID)
	if !ok {
		return config.BackendDef{}, "", derrors.New(derrors.CodeUnavailable, op, errBackendNotFound).With("backend", backendID)
	}
	return b, url, nil
}

// resolveTarget is lookupBackend plus the backend's credential.
func (s *shared) resolveTarget(op, backendID, downstream string) (backendTarget, error) {
	b, url, err := s.lookupBackend(op, backendID)
	if err != nil {
		return backendTarget{}, err
	}
	auth, err := backendAuth(b)
	if err != nil {
		return backendTarget{}, err
	}
	return backendTarget{id: backendID, downstream: downstream, url: url, auth: auth, backend: b}, nil
}
