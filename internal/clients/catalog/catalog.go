// Package catalog holds what the provider clients share: the OpenAI Models API
// shapes, a TTL cache, a strict list with stale-cache fallback, a process-scoped
// client registry, one capped JSON GET with status mapping, and the small text
// helpers their error messages use.
package catalog

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const (
	// ObjectList is the OpenAI list object type.
	ObjectList = "list"
	// ObjectModel is the OpenAI model object type.
	ObjectModel = "model"
)

// Model is the OpenAI Models API object (bare downstream id for gateway prefixing).
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ModelList is the OpenAI list response for GET /v1/models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Cache keeps the last successful catalog fetch for a TTL. Reads return a copy.
type Cache struct {
	mu     sync.Mutex
	models []Model
	at     time.Time
	ttl    time.Duration
	now    func() time.Time
}

// NewCacheClock returns an empty cache that serves entries younger than ttl, aged by
// now (the gateway's clock; tests step it past the TTL). Panics on a nil clock.
func NewCacheClock(ttl time.Duration, now func() time.Time) *Cache {
	if now == nil {
		panic("catalog.NewCacheClock: nil clock")
	}
	return &Cache{ttl: ttl, now: now}
}

// Get returns a copy of the cached models when they are present and fresh.
func (c *Cache) Get() ([]Model, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.models) == 0 {
		return nil, false
	}
	if c.now().Sub(c.at) > c.ttl {
		return nil, false
	}
	out := make([]Model, len(c.models))
	copy(out, c.models)
	return out, true
}

// GetStale returns a copy of the cached models whatever their age: the fallback
// when a fetch fails.
func (c *Cache) GetStale() ([]Model, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.models) == 0 {
		return nil, false
	}
	out := make([]Model, len(c.models))
	copy(out, c.models)
	return out, true
}

// Put replaces the cached models.
func (c *Cache) Put(models []Model) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.models = append([]Model(nil), models...)
	c.at = c.now()
}

// Source is one backend's model catalog: where it is cached and how it is fetched.
type Source struct {
	Provider string
	Backend  string
	Cache    *Cache
	Fetch    func(context.Context) ([]Model, error)
	// Log receives the stale-cache warning; nil discards it.
	Log *slog.Logger
}

// ListStrict serves a fresh cache hit, else fetches and caches. When the fetch fails it
// falls back to a cached copy (and warns), and returns the error only when none exists.
func ListStrict(ctx context.Context, src Source) (ModelList, error) {
	if src.Cache == nil || src.Fetch == nil {
		return ModelList{Object: ObjectList}, derrors.New(derrors.CodeInternal, "catalog.ListStrict", "cache and fetch required")
	}
	cache := src.Cache
	if cached, ok := cache.Get(); ok {
		return ModelList{Object: ObjectList, Data: cached}, nil
	}
	models, err := src.Fetch(ctx)
	if err != nil {
		if cached, ok := cache.GetStale(); ok {
			if src.Log != nil {
				src.Log.Warn("polypus: models catalog fetch failed; using stale cache",
					"provider", src.Provider, "backend", src.Backend, "cached_models", len(cached), "err", err)
			}
			return ModelList{Object: ObjectList, Data: cached}, nil
		}
		return ModelList{Object: ObjectList}, err
	}
	cache.Put(models)
	return ModelList{Object: ObjectList, Data: models}, nil
}

// Registry keeps one client per key (a backend id) for the life of the process. Each
// entry remembers a fingerprint of what the client was built from (its token and API
// base); when that changes, Get builds a new client and drops the old one, so a rotated
// token neither keeps the old client alive nor grows the map.
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

// Reset drops every client (tests only).
func (r *Registry[T]) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients = nil
}

// StatusCode maps an upstream HTTP status to the closest domain code.
func StatusCode(status int) derrors.Code {
	switch {
	case status == http.StatusTooManyRequests:
		return derrors.CodeRateLimited
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return derrors.CodeUnauthorized
	case status == http.StatusNotFound || status == http.StatusGone:
		return derrors.CodeNotFound
	case status >= 400 && status < 500:
		return derrors.CodeInvalid
	default:
		return derrors.CodeUnavailable
	}
}

// Truncate trims s and caps it at n runes for error messages, ending a cut string
// with "...". It cuts on rune boundaries, so upstream text stays valid UTF-8.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:max(0, n)]) + "..."
}

// FirstNonEmpty returns the first value that is not blank, trimmed, or "".
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
