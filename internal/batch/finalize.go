package batch

import (
	"errors"
	"io/fs"
	"strings"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// FinalizePatch is the durable outcome of an upstream batch that has finished.
type FinalizePatch struct {
	OkJSONL       []byte
	ErrJSONL      []byte
	RequestCounts RequestCounts
	Status        Status
	CompletedAt   int64
	FailedAt      int64
	CancelledAt   int64
}

func statusAllowsFinalize(st Status) bool {
	return st == StatusValidating || st == StatusInProgress || st == StatusCancelling
}

// TryFinalizeBatch atomically writes output files and terminal metadata when the batch is still open.
// Returns applied=false when another caller already finalized or the batch is terminal/cancelled.
func (s *DiskStore) TryFinalizeBatch(id string, patch FinalizePatch) (Meta, bool, error) {
	if s == nil {
		return Meta{}, false, derrors.New(derrors.CodeFailedPrecondition, "batch.TryFinalizeBatch", "not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Meta{}, false, derrors.New(derrors.CodeInvalid, "batch.TryFinalizeBatch", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readBatchMetaUnlocked(id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Meta{}, false, derrors.New(derrors.CodeNotFound, "batch.TryFinalizeBatch", "batch not found")
		}
		return Meta{}, false, derrors.Wrap(err, derrors.CodeInternal, "batch.TryFinalizeBatch", "read")
	}
	if !statusAllowsFinalize(meta.Status) {
		return meta, false, nil
	}

	// A failure after an output file is saved removes the files this call wrote, so
	// nothing unreferenced is left under files/.
	var written []string
	fail := func(err error) (Meta, bool, error) {
		for _, id := range written {
			err = errors.Join(err, s.removeFileUnlocked(id))
		}
		return meta, false, err
	}
	save := func(name string, content []byte) (string, error) {
		rec := &FileRecord{Filename: meta.ID + name, Purpose: FilePurposeBatch}
		if err := s.prepareFileRecordLocked(rec, content); err != nil {
			return "", err
		}
		if err := s.saveFileUnlocked(rec, content); err != nil {
			return "", err
		}
		written = append(written, rec.ID)
		return rec.ID, nil
	}
	meta.RequestCounts = patch.RequestCounts
	if len(patch.OkJSONL) > 0 {
		if meta.OutputFileID, err = save("_output.jsonl", patch.OkJSONL); err != nil {
			return fail(err)
		}
	}
	if len(patch.ErrJSONL) > 0 {
		if meta.ErrorFileID, err = save("_error.jsonl", patch.ErrJSONL); err != nil {
			return fail(err)
		}
	}
	meta.Status = patch.Status
	meta.CompletedAt = patch.CompletedAt
	meta.FailedAt = patch.FailedAt
	meta.CancelledAt = patch.CancelledAt
	if err := s.writeBatchMeta(&meta); err != nil {
		return fail(err)
	}
	return meta, true, nil
}
