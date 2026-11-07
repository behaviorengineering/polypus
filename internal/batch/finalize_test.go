package batch

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestTryFinalizeBatchConcurrent(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	store, err := NewDiskStore(root, func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	meta := &Meta{
		Status:      StatusInProgress,
		InputFileID: "file-in",
		Endpoint:    EndpointChatCompletions,
	}
	if err := store.SaveBatch(t.Context(), meta); err != nil {
		t.Fatal(err)
	}
	patch := FinalizePatch{
		OkJSONL:       []byte(`{"custom_id":"a"}` + "\n"),
		RequestCounts: RequestCounts{Total: 1, Completed: 1},
		Status:        StatusCompleted,
		CompletedAt:   fixed.Unix(),
	}
	var wg sync.WaitGroup
	applied := make([]bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, ok, err := store.TryFinalizeBatch(t.Context(), meta.ID, patch)
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
	final, err := store.GetBatch(t.Context(), meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != StatusCompleted || final.OutputFileID == "" {
		t.Fatalf("final: %+v", final)
	}
}

func TestTryFinalizeBatchRejectsPathIDs(t *testing.T) {
	root := t.TempDir()
	store, err := NewDiskStore(root+"/store", func() time.Time { return time.Unix(1, 0) })
	if err != nil {
		t.Fatal(err)
	}
	// A batch meta outside the store root that a "../" id would reach.
	if err := os.WriteFile(filepath.Join(root, "x.json"), []byte(`{"id":"x","status":"in_progress"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../../x", "a/b", `..\x`, " "} {
		_, applied, err := store.TryFinalizeBatch(t.Context(), id, FinalizePatch{Status: StatusCompleted})
		if applied || derrors.CodeOf(err) != derrors.CodeInvalid {
			t.Fatalf("id %q: applied=%v err=%v, want CodeInvalid", id, applied, err)
		}
	}
}

func TestStatusTerminal(t *testing.T) {
	for st, want := range map[Status]bool{
		StatusCompleted: true, StatusFailed: true, StatusCancelled: true, StatusExpired: true,
		StatusValidating: false, StatusInProgress: false, StatusCancelling: false, Status("new"): false,
	} {
		if st.Terminal() != want {
			t.Fatalf("%q.Terminal() = %v", st, !want)
		}
	}
}

func TestStoreErrorsNameTheCaller(t *testing.T) {
	store, err := NewDiskStore(t.TempDir(), func() time.Time { return time.Unix(1, 0) })
	if err != nil {
		t.Fatal(err)
	}
	err = store.UpdateBatch(t.Context(), Meta{ID: "missing-dir/../x"})
	if err == nil || !strings.Contains(err.Error(), "batch.UpdateBatch") {
		t.Fatalf("want the UpdateBatch op, got %v", err)
	}
	if err := store.DeleteFile(t.Context(), "file_none"); derrors.CodeOf(err) != derrors.CodeNotFound || !strings.Contains(err.Error(), "batch.DeleteFile") {
		t.Fatalf("want a DeleteFile not-found, got %v", err)
	}
}
