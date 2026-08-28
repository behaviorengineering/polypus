package gateway

import (
	"context"
	"sync"
	"time"
)

const switchyardProbeTTL = 5 * time.Second

type switchyardProbeCache struct {
	mu      sync.Mutex
	url     string
	checked time.Time
	err     error
}

func (c *switchyardProbeCache) available(ctx context.Context, baseURL string) error {
	if c == nil {
		return probeSwitchyard(ctx, baseURL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.url == baseURL && !c.checked.IsZero() && time.Since(c.checked) < switchyardProbeTTL {
		return c.err
	}
	c.url = baseURL
	c.err = probeSwitchyard(ctx, baseURL)
	c.checked = time.Now()
	return c.err
}

func (c *switchyardProbeCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checked = time.Time{}
}
