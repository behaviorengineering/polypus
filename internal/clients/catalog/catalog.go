// Package catalog holds what the provider model-catalog clients share: the OpenAI
// Models API shapes, a TTL cache, a strict list with stale-cache fallback, and a
// process-scoped client registry.
package catalog

import (
	"context"
	"log/slog"
	"sync"
	"time"
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
}

// NewCache returns an empty cache that serves entries younger than ttl.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl}
}

// Get returns a copy of the cached models when they are present and fresh.
func (c *Cache) Get() ([]Model, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.models) == 0 {
		return nil, false
	}
	if time.Since(c.at) > c.ttl {
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
	c.at = time.Now()
}

// ListStrict serves a fresh cache hit, else fetches and caches. When the fetch fails it
// falls back to a cached copy, and returns the error only when none exists.
func ListStrict(ctx context.Context, provider string, cache *Cache, fetch func(context.Context) ([]Model, error)) (ModelList, error) {
	if cached, ok := cache.Get(); ok {
		return ModelList{Object: ObjectList, Data: cached}, nil
	}
	models, err := fetch(ctx)
	if err != nil {
		if cached, ok := cache.Get(); ok {
			slog.Warn(provider+": models catalog fetch failed; using stale cache",
				"err", err,
				"cached_models", len(cached),
			)
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

// Truncate shortens s to at most n bytes for error messages.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
