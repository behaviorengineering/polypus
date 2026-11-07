package gemini

import (
	"log/slog"
	"time"

	"github.com/behaviorengineering/polypus/internal/clients/clientcache"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

const opGet = "gemini.Clients.Get"

// Clients caches one catalog client per backend; a changed credential or URL replaces
// it. A gateway owns one; its clients share the gateway's outbound policies and clock.
type Clients struct {
	Policies *outbound.Policies
	// Logger receives the clients' warnings; nil discards them.
	Logger *slog.Logger
	// Now ages the catalog caches. Required: Get fails without it.
	Now func() time.Time
	reg clientcache.Registry[Client]
}

// Get returns the cached client for b, building it on first use.
func (cs *Clients) Get(b config.BackendDef) (*Client, error) {
	if cs == nil || cs.Now == nil {
		return nil, derrors.New(derrors.CodeInternal, opGet, "clients need a clock").With("backend", b.ID)
	}
	if !b.IsGeminiExtension() {
		return nil, derrors.Wrap(errNotGeminiExtension, derrors.CodeInvalid, opGet, "backend").With("backend", b.ID)
	}
	listURL := openAIModelsListURL(b.BaseURL)
	return cs.reg.ForBackend(opGet, b, listURL, func(token string) (*Client, error) {
		c, err := newClient(listURL, token, cs.Now)
		if err != nil {
			return nil, err
		}
		c.backendID = b.ID
		c.policies = cs.Policies
		c.log = cs.Logger
		return c, nil
	})
}
