package cloudflare

import (
	"log/slog"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/clientcache"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const opGet = "cloudflare.Clients.Get"

// Clients caches one Workers AI client per backend id. A changed bearer token or API
// base replaces it, so a rotated token gets a fresh client and the old one is dropped.
// A gateway owns one, shared with its router's speech plugin; its clients share the
// gateway's outbound policies and clock.
type Clients struct {
	Policies *outbound.Policies
	// Logger receives the clients' warnings; nil discards them.
	Logger *slog.Logger
	// Now ages the catalog caches. Required: Get fails without it.
	Now func() time.Time
	// Speech holds the speech defaults; an empty field takes the built-in default.
	Speech SpeechDefaults
	reg    clientcache.Registry[Client]
}

// Get returns the cached client for b, building it on first use.
func (cs *Clients) Get(b config.BackendDef) (*Client, error) {
	if cs == nil || cs.Now == nil {
		return nil, derrors.New(derrors.CodeInternal, opGet, "clients need a clock").With("backend", b.ID)
	}
	if !b.IsCloudflareExtension() {
		return nil, derrors.Wrap(errNotCloudflareExtension, derrors.CodeInvalid, opGet, "backend").With("backend", b.ID)
	}
	apiBase, err := AIBaseURL(b.BaseURL)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInvalid, opGet, "base url").With("backend", b.ID)
	}
	return cs.reg.ForBackend(opGet, b, apiBase, func(token string) (*Client, error) {
		c, err := newClient(apiBase, token, cs.Now, cs.Speech)
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
