// Package catalog holds what the provider model catalogues share: the OpenAI Models
// API shapes, a TTL cache and a strict list with stale-cache fallback.
package catalog

import (
	"context"
	"log/slog"
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

// FirstNonEmpty returns the first value that is not blank, trimmed, or "". It is the
// last text helper left here, kept while internal/gateway/batches imports it.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
