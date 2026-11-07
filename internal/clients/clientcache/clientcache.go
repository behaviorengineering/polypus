// Package clientcache keeps one provider client per backend for the life of its
// owner (each gateway's Clients value holds one Registry), and rebuilds a client
// when the token or URL it was built from changes.
package clientcache

import (
	"crypto/sha256"
	"sync"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// Registry keeps one client per key (a backend id) for the life of its owner. Each
// entry remembers a fingerprint of what the client was built from (its token and API
// base); when that changes, Get builds a new client and drops the old one, so a rotated
// token neither keeps the old client alive nor grows the map. The zero value is ready
// to use.
type Registry[T any] struct {
	mu      sync.Mutex
	clients map[string]registryEntry[T]
}

// registryEntry is one cached client and the fingerprint it was built from. The
// fingerprint is a hash, so the registry holds no token of its own.
type registryEntry[T any] struct {
	fingerprint [sha256.Size]byte
	client      *T
}

// Get returns the client for key when it was built from the same fingerprint, else
// builds one with create and replaces the entry. fingerprint carries every input
// that makes a client stale, such as token + "\x00" + base.
func (r *Registry[T]) Get(key, fingerprint string, create func() (*T, error)) (*T, error) {
	fp := sha256.Sum256([]byte(fingerprint))
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.clients[key]; ok && e.fingerprint == fp {
		return e.client, nil
	}
	c, err := create()
	if err != nil {
		return nil, err
	}
	if r.clients == nil {
		r.clients = map[string]registryEntry[T]{}
	}
	r.clients[key] = registryEntry[T]{fingerprint: fp, client: c}
	return c, nil
}

// ForBackend is the shared body of each provider's Clients.Get: it resolves b's bearer
// token and returns the client cached for b.ID, building one with build(token) when
// the token or base changed. base is the URL the client talks to. Errors carry op and
// the backend id, and keep any domain code build returns.
func (r *Registry[T]) ForBackend(op string, b config.BackendDef, base string, build func(token string) (*T, error)) (*T, error) {
	if r == nil || build == nil {
		return nil, derrors.New(derrors.CodeInternal, op, "registry and build required").With("backend", b.ID)
	}
	token, err := b.Auth.ResolveBearerToken()
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "resolve token").With("backend", b.ID)
	}
	return r.Get(b.ID, token+"\x00"+base, func() (*T, error) {
		c, err := build(token)
		if err != nil {
			return nil, derrors.Wrap(err, derrors.CodeOr(err, derrors.CodeInvalid), op, "build client").With("backend", b.ID)
		}
		return c, nil
	})
}
