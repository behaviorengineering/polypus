package gateway

import (
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func bearerAuthHeader(b config.BackendDef) (string, error) {
	if !b.Remote {
		return "", nil
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeNotReady, "gateway.bearerAuthHeader", "resolve bearer")
	}
	return "Bearer " + token, nil
}

func backendAuth(b config.BackendDef) (string, error) {
	auth, err := bearerAuthHeader(b)
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeNotReady, "gateway.backendAuth", "backend auth")
	}
	return auth, nil
}
