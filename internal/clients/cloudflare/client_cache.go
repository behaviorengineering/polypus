package cloudflare

import (
	"log/slog"

	derrors "github.com/behaviorengineering/polypus/internal/errors"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

// Clients caches one Workers AI client per backend id, resolved bearer token and API
// base, so a rotated token gets a fresh client. A gateway owns one, shared with its
// router's speech plugin; its clients share the gateway's outbound policies.
type Clients struct {
	Policies *outbound.Policies
	// Logger receives the clients' warnings; nil discards them.
	Logger *slog.Logger
	reg    catalog.Registry[Client]
}

// Get returns the cached client for b, building it on first use.
func (cs *Clients) Get(b config.BackendDef) (*Client, error) {
	if !b.IsCloudflareExtension() {
		return nil, derrors.Wrap(errNotCloudflareExtension, derrors.CodeInvalid, "cloudflare", "backend").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, err
	}
	apiBase, err := AIBaseURL(b.BaseURL)
	if err != nil {
		return nil, err
	}
	return cs.reg.Get(b.ID+"\x00"+token+"\x00"+apiBase, func() (*Client, error) {
		c, err := newClient(apiBase, token)
		if err != nil {
			return nil, err
		}
		c.backendID = b.ID
		c.policies = cs.Policies
		c.log = cs.Logger
		return c, nil
	})
}

// dep is this client's outbound dependency name, scoped to its backend.
func (c *Client) dep() string {
	return outbound.Scoped(outbound.DepCloudflare, c.backendID)
}
