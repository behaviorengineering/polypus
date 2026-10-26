package batch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestDiskStoreFileRoundTrip(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	store, err := NewDiskStore(root, func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	rec := &FileRecord{
		Filename: "in.jsonl",
		Purpose:  FilePurposeBatch,
	}
	content := []byte(`{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{}}`)
	if err := store.SaveFile(rec, content); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetFile(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bytes != int64(len(content)) || got.CreatedAt != fixed.Unix() {
		t.Fatalf("meta mismatch: %+v", got)
	}
	raw, err := store.ReadFileContent(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(content) {
		t.Fatalf("content mismatch")
	}
}

func TestDiskStoreRejectsIDsOutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "store")
	store, err := NewDiskStore(root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	// A file beside the store that a traversal id would reach.
	outside := filepath.Join(parent, "secret.json")
	if err := os.WriteFile(outside, []byte(`{"id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../../secret", "../secret.json", "a/b", `a\b`, "..", "file_ok\x00"} {
		var de *derrors.Error
		if _, err := store.GetFile(id); !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
			t.Errorf("GetFile(%q): want CodeInvalid, got %v", id, err)
		}
		if _, err := store.ReadFileContent(id); !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
			t.Errorf("ReadFileContent(%q): want CodeInvalid, got %v", id, err)
		}
		if err := store.DeleteFile(id); !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
			t.Errorf("DeleteFile(%q): want CodeInvalid, got %v", id, err)
		}
		if _, err := store.GetBatch(id); !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
			t.Errorf("GetBatch(%q): want CodeInvalid, got %v", id, err)
		}
		if err := store.UpdateBatch(Meta{ID: id}); !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
			t.Errorf("UpdateBatch(%q): want CodeInvalid, got %v", id, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("file outside the store root was touched: %v", err)
	}
}

func TestDeleteFileReferencedByBatch(t *testing.T) {
	root := t.TempDir()
	fixed := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	store, err := NewDiskStore(root, func() time.Time { return fixed })
	if err != nil {
		t.Fatal(err)
	}
	rec := &FileRecord{Filename: "in.jsonl", Purpose: FilePurposeBatch}
	if err := store.SaveFile(rec, []byte("x")); err != nil {
		t.Fatal(err)
	}
	meta := &Meta{
		Status:      StatusInProgress,
		InputFileID: rec.ID,
		Endpoint:    EndpointChatCompletions,
	}
	if err := store.SaveBatch(meta); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFile(rec.ID); err == nil {
		t.Fatal("expected conflict deleting referenced file")
	}
}

func TestParseBatchJSONLSingleModel(t *testing.T) {
	raw := []byte(`{"custom_id":"r1","method":"POST","url":"/v1/chat/completions","body":{"model":"cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"hi"}]}}
{"custom_id":"r2","method":"POST","url":"/v1/chat/completions","body":{"model":"cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast","messages":[{"role":"user","content":"bye"}]}}`)
	parsed, err := ParseBatchJSONL(raw, EndpointChatCompletions)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Model != "cf_local/@cf/meta/llama-3.3-70b-instruct-fp8-fast" || len(parsed.Lines) != 2 {
		t.Fatalf("parsed: %+v", parsed)
	}
}

func TestDeleteFileFailsClosedOnUnreadableBatch(t *testing.T) {
	root := t.TempDir()
	store, err := NewDiskStore(root, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	rec := &FileRecord{Filename: "in.jsonl", Purpose: FilePurposeBatch}
	if err := store.SaveFile(rec, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, batchMetaDir, "batch_corrupt.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteFile(rec.ID); err == nil {
		t.Fatal("an unreadable batch may reference the file; delete must refuse")
	}
	if _, err := store.GetFile(rec.ID); err != nil {
		t.Fatalf("file must still exist: %v", err)
	}
	if _, err := store.ListBatches(); err == nil {
		t.Fatal("ListBatches must surface the corrupt batch")
	}
}

func TestDeleteFileMissingIsNotFound(t *testing.T) {
	store, err := NewDiskStore(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var de *derrors.Error
	if err := store.DeleteFile("file_missing"); !errors.As(err, &de) || de.Code() != derrors.CodeNotFound {
		t.Fatalf("want CodeNotFound, got %v", err)
	}
}

func TestParseBatchJSONLRejectsDuplicateCustomID(t *testing.T) {
	line := `{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{"model":"m","messages":[{"role":"user","content":"hi"}]}}`
	if _, err := ParseBatchJSONL([]byte(line+"\n"+line), EndpointChatCompletions); err == nil {
		t.Fatal("duplicate custom_id must be rejected")
	}
}
