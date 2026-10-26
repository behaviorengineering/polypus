// Package catalog holds what the provider model-catalog clients share: the OpenAI
// Models API shapes, a TTL cache, a strict list with stale-cache fallback, and a
// process-scoped client registry.
package catalog

import (
	"context"
	"log/slog"
	"net/http"
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

// NewCache returns an empty cache that serves entries younger than ttl.
func NewCache(ttl time.Duration) *Cache {
	return NewCacheClock(ttl, time.Now)
}

// NewCacheClock is NewCache with an injected clock (tests step past the TTL).
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

// Registry keeps one client per key for the life of the process.
type Registry[T any] struct {
	mu      sync.Mutex
	clients map[string]*T
}

// Get returns the client for key, creating it once with create.
func (r *Registry[T]) Get(key string, create func() (*T, error)) (*T, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.clients[key]; ok {
		return c, nil
	}
	c, err := create()
	if err != nil {
		return nil, err
	}
	if r.clients == nil {
		r.clients = map[string]*T{}
	}
	r.clients[key] = c
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

// Truncate shortens s to at most n bytes for error messages.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
