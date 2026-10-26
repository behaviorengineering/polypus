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
		if err := store.UpdateBatch(BatchMeta{ID: id}); !errors.As(err, &de) || de.Code() != derrors.CodeInvalid {
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
	meta := &BatchMeta{
		Status:      BatchStatusInProgress,
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
