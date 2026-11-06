package anthropic

import (
	"log/slog"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/catalog"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const opGet = "anthropic.Clients.Get"

// Clients caches one catalog client per backend, credential and URL. A gateway owns
// one; its clients share the gateway's outbound policies and clock.
type Clients struct {
	Policies *outbound.Policies
	// Logger receives the clients' warnings; nil discards them.
	Logger *slog.Logger
	// Now ages the catalog caches. Required: Get fails without it.
	Now func() time.Time
	reg catalog.Registry[Client]
}

// Get returns the cached client for b, building it on first use.
func (cs *Clients) Get(b config.BackendDef) (*Client, error) {
	if cs == nil || cs.Now == nil {
		return nil, derrors.New(derrors.CodeInternal, opGet, "clients need a clock").With("backend", b.ID)
	}
	if !b.IsAnthropicExtension() {
		return nil, derrors.Wrap(errNotAnthropicExtension, derrors.CodeInvalid, opGet, "backend").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), opGet, "resolve token").With("backend", b.ID)
	}
	listURL := modelsListURL(b.BaseURL)
	key := b.ID + "\x00" + token + "\x00" + listURL
	return cs.reg.Get(key, func() (*Client, error) {
		c, err := newClient(listURL, token, cs.Now)
		if err != nil {
			return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), opGet, "build client").With("backend", b.ID)
		}
		c.backendID = b.ID
		c.policies = cs.Policies
		c.log = cs.Logger
		return c, nil
	})
}
