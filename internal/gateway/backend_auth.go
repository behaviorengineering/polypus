package gateway

import (
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// bearerAuthHeader is the Authorization header a remote backend gets: its own bearer
// token. A local backend gets none.
func bearerAuthHeader(b config.BackendDef) (string, error) {
	if !b.Remote {
		return "", nil
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return "", derrors.Wrap(err, derrors.CodeNotReady, "gateway.bearerAuthHeader", "resolve bearer").With("backend", b.ID)
	}
	return "Bearer " + token, nil
}
