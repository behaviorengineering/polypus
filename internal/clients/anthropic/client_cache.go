package anthropic

import (
	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Clients caches one catalog client per backend, credential and URL. A gateway owns
// one; its clients share the gateway's outbound policies.
type Clients struct {
	Policies *outbound.Policies
	reg      catalog.Registry[Client]
}

// Get returns the cached client for b, building it on first use.
func (cs *Clients) Get(b config.BackendDef) (*Client, error) {
	if !b.IsAnthropicExtension() {
		return nil, derrors.Wrap(errNotAnthropicExtension, derrors.CodeInvalid, "anthropic", "backend").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	listURL := modelsListURL(b.BaseURL)
	key := b.ID + "\x00" + token + "\x00" + listURL
	return cs.reg.Get(key, func() (*Client, error) {
		c, err := newClient(listURL, token)
		if err != nil {
			return nil, err
		}
		c.backendID = b.ID
		c.policies = cs.Policies
		return c, nil
	})
}
