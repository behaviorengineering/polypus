package batch

import (
	"sync"
	"testing"
	"time"
)

func TestTryFinalizeBatchConcurrent(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	store, err := NewDiskStore(root, func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	meta := &BatchMeta{
		Status:      BatchStatusInProgress,
		InputFileID: "file-in",
		Endpoint:    EndpointChatCompletions,
	}
	if err := store.SaveBatch(meta); err != nil {
		t.Fatal(err)
	}
	patch := FinalizePatch{
		OkJSONL:       []byte(`{"custom_id":"a"}` + "\n"),
		RequestCounts: RequestCounts{Total: 1, Completed: 1},
		Status:        BatchStatusCompleted,
		CompletedAt:   fixed.Unix(),
	}
	var wg sync.WaitGroup
	applied := make([]bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, ok, err := store.TryFinalizeBatch(meta.ID, patch)
			if err != nil {
				t.Errorf("finalize: %v", err)
				return
			}
			applied[idx] = ok
		}(i)
	}
	wg.Wait()
	count := 0
	for _, ok := range applied {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one applied, got %d (%v)", count, applied)
	}
	final, err := store.GetBatch(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != BatchStatusCompleted || final.OutputFileID == "" {
		t.Fatalf("final: %+v", final)
	}
}
