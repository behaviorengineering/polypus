package gateway

import (
	"fmt"

	"github.com/behaviorengineering/polypus/internal/config"
)

func bearerAuthHeader(b config.BackendDef) (string, error) {
	if !b.Remote {
		return "", nil
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return "", err
	}
	return "Bearer " + token, nil
}

func mustBackendAuth(b config.BackendDef) (string, error) {
	auth, err := bearerAuthHeader(b)
	if err != nil {
		return "", fmt.Errorf("backend auth: %w", err)
	}
	return auth, nil
}
