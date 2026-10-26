package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestModelsCacheFlushesOncePerPass(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models-inventory.json")
	c := &modelsInventoryCache{byKey: map[string]modelsCacheEntry{}, ttl: time.Hour, log: testShared.log, path: path, now: time.Now}
	c.put("a", []openaiModel{{ID: "a/m1"}})
	c.put("b", []openaiModel{{ID: "b/m1"}})
	if _, err := os.Stat(path); err == nil {
		t.Fatal("put must not write the file; flush does")
	}
	c.flush()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: test temp dir
	if err != nil {
		t.Fatal(err)
	}
	var disk map[string]modelsCacheEntry
	if err := json.Unmarshal(raw, &disk); err != nil || len(disk) != 2 {
		t.Fatalf("flushed cache: %s (%v)", raw, err)
	}
	reloaded := &modelsInventoryCache{byKey: map[string]modelsCacheEntry{}, ttl: time.Hour, log: testShared.log, path: path, now: time.Now}
	reloaded.loadDisk()
	if got, ok := reloaded.get("b"); !ok || len(got) != 1 {
		t.Fatalf("reloaded entry: %v %v", got, ok)
	}
}

func TestModelsCacheConcurrentPutAndFlush(t *testing.T) {
	c := &modelsInventoryCache{byKey: map[string]modelsCacheEntry{}, ttl: time.Hour, log: testShared.log, path: filepath.Join(t.TempDir(), "m.json"), now: time.Now}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.put(strconv.Itoa(i), []openaiModel{{ID: "x"}})
			c.flush()
		}(i)
	}
	wg.Wait()
	c.flush()
	if c.dirty {
		t.Fatal("a final flush must leave the cache clean")
	}
}

func TestStreamChunkDumpsAreCappedPerStream(t *testing.T) {
	dir := t.TempDir()
	ctx := withStreamChunkCounter(context.Background(), testShared.log, dir)
	for i := range maxChunkDumpsPerStream + 5 {
		dumpIllegalStreamChunk(ctx, i, []byte(`{"x":1}`), nil)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != maxChunkDumpsPerStream {
		t.Fatalf("want %d dumps for one stream, got %d", maxChunkDumpsPerStream, len(entries))
	}
}
