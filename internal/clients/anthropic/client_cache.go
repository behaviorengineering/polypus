package anthropic

import (
	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
)

var clients catalog.Registry[Client]

// GetClient returns a process-scoped Anthropic catalog client for b.
func GetClient(b config.BackendDef) (*Client, error) {
	if !b.IsAnthropicExtension() {
		return nil, derrors.Wrap(errNotAnthropicExtension, derrors.CodeInvalid, "anthropic", "backend").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	listURL := modelsListURL(b.BaseURL)
	key := b.ID + "\x00" + token + "\x00" + listURL
	return clients.Get(key, func() (*Client, error) {
		return newClient(listURL, token)
	})
}
