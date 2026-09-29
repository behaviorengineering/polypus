package batch

import (
	"testing"
	"time"
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
