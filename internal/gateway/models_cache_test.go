package gateway

import (
	"testing"
	"time"
)

func TestModelsInventoryCacheTTLUsesClock(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	c := &modelsInventoryCache{
		byKey: make(map[string]modelsCacheEntry),
		ttl:   time.Minute,
		now:   func() time.Time { return now },
	}
	c.put("leaf", []openaiModel{{ID: "leaf/a", Object: "model"}})
	if _, ok := c.get("leaf"); !ok {
		t.Fatal("expected hit at put time")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.get("leaf"); ok {
		t.Fatal("expected miss after TTL")
	}
}
