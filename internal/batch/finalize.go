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
	Status        BatchStatus
	CompletedAt   int64
	FailedAt      int64
	CancelledAt   int64
}

func isTerminalBatchStatus(st BatchStatus) bool {
	switch st {
	case BatchStatusCompleted, BatchStatusFailed, BatchStatusCancelled, BatchStatusExpired:
		return true
	case BatchStatusValidating, BatchStatusInProgress, BatchStatusCancelling:
		return false
	default:
		return false
	}
}

func statusAllowsFinalize(st BatchStatus) bool {
	return st == BatchStatusValidating || st == BatchStatusInProgress || st == BatchStatusCancelling
}

// TryFinalizeBatch atomically writes output files and terminal metadata when the batch is still open.
// Returns applied=false when another caller already finalized or the batch is terminal/cancelled.
func (s *DiskStore) TryFinalizeBatch(id string, patch FinalizePatch) (BatchMeta, bool, error) {
	if s == nil {
		return BatchMeta{}, false, derrors.New(derrors.CodeFailedPrecondition, "batch.TryFinalizeBatch", "not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return BatchMeta{}, false, derrors.New(derrors.CodeInvalid, "batch.TryFinalizeBatch", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, err := s.readBatchMetaUnlocked(id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return BatchMeta{}, false, derrors.New(derrors.CodeNotFound, "batch.TryFinalizeBatch", "batch not found")
		}
		return BatchMeta{}, false, derrors.Wrap(err, derrors.CodeInternal, "batch.TryFinalizeBatch", "read")
	}
	if isTerminalBatchStatus(meta.Status) {
		return meta, false, nil
	}
	if !statusAllowsFinalize(meta.Status) {
		return meta, false, nil
	}

	meta.RequestCounts = patch.RequestCounts
	if len(patch.OkJSONL) > 0 {
		rec := &FileRecord{Filename: meta.ID + "_output.jsonl", Purpose: FilePurposeBatch}
		if err := s.prepareFileRecordLocked(rec, patch.OkJSONL); err != nil {
			return meta, false, err
		}
		if err := s.saveFileUnlocked(rec, patch.OkJSONL); err != nil {
			return meta, false, err
		}
		meta.OutputFileID = rec.ID
	}
	if len(patch.ErrJSONL) > 0 {
		rec := &FileRecord{Filename: meta.ID + "_error.jsonl", Purpose: FilePurposeBatch}
		if err := s.prepareFileRecordLocked(rec, patch.ErrJSONL); err != nil {
			return meta, false, err
		}
		if err := s.saveFileUnlocked(rec, patch.ErrJSONL); err != nil {
			return meta, false, err
		}
		meta.ErrorFileID = rec.ID
	}
	meta.Status = patch.Status
	meta.CompletedAt = patch.CompletedAt
	meta.FailedAt = patch.FailedAt
	meta.CancelledAt = patch.CancelledAt
	if err := s.writeBatchMeta(&meta); err != nil {
		return meta, false, err
	}
	return meta, true, nil
}
