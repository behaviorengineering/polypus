package batch

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const (
	fileMetaDir    = "files"
	fileContentDir = "files/content"
	batchMetaDir   = "batches"
)

// DiskStore persists OpenAI-shaped files and batch metadata under a root directory.
type DiskStore struct {
	root string
	mu   sync.Mutex
	now  func() time.Time
}

// NewDiskStore creates a store at root. now must be non-nil (inject at gateway entry).
func NewDiskStore(root string, now func() time.Time) (*DiskStore, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" {
		return nil, derrors.New(derrors.CodeInvalid, "batch.NewDiskStore", "root required")
	}
	if now == nil {
		return nil, derrors.New(derrors.CodeInvalid, "batch.NewDiskStore", "clock required")
	}
	for _, sub := range []string{fileMetaDir, fileContentDir, batchMetaDir} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o700); err != nil {
			return nil, derrors.Wrap(err, derrors.CodeInternal, "batch.NewDiskStore", "mkdir")
		}
	}
	return &DiskStore{root: root, now: now}, nil
}

func (s *DiskStore) unixNow() int64 {
	return s.now().UTC().Unix()
}

// SaveFile stores content and metadata; rec sets ID and timestamps when empty.
func (s *DiskStore) SaveFile(rec *FileRecord, content []byte) error {
	if s == nil || rec == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "batch.SaveFile", "not configured")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.prepareFileRecordLocked(rec, content); err != nil {
		return err
	}
	return s.saveFileUnlocked(rec, content)
}

func (s *DiskStore) prepareFileRecordLocked(rec *FileRecord, content []byte) error {
	if rec == nil {
		return derrors.New(derrors.CodeInvalid, "batch.prepareFileRecordLocked", "record required")
	}
	if rec.ID == "" {
		rec.ID = newID("file")
	}
	if rec.Object == "" {
		rec.Object = "file"
	}
	if rec.CreatedAt == 0 {
		rec.CreatedAt = s.unixNow()
	}
	if rec.Status == "" {
		rec.Status = "processed"
	}
	rec.Bytes = int64(len(content))
	return nil
}

func (s *DiskStore) saveFileUnlocked(rec *FileRecord, content []byte) error {
	if _, err := checkID("batch.SaveFile", rec.ID); err != nil {
		return err
	}
	contentPath := filepath.Join(s.root, fileContentDir, rec.ID)
	if err := writeFileAtomic(contentPath, content); err != nil {
		return err
	}
	metaPath := filepath.Join(s.root, fileMetaDir, rec.ID+".json")
	raw, err := json.Marshal(rec)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "batch.SaveFile", "marshal meta")
	}
	if err := writeFileAtomic(metaPath, raw); err != nil {
		return err
	}
	return nil
}

// GetFile returns file metadata.
func (s *DiskStore) GetFile(id string) (FileRecord, error) {
	if s == nil {
		return FileRecord{}, derrors.New(derrors.CodeFailedPrecondition, "batch.GetFile", "not configured")
	}
	id, err := checkID("batch.GetFile", id)
	if err != nil {
		return FileRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readFileMeta("batch.GetFile", id)
}

// readFileMeta reads a file's metadata; op names the caller in errors. id must have
// passed checkID.
func (s *DiskStore) readFileMeta(op, id string) (FileRecord, error) {
	var rec FileRecord
	if err := readJSON(filepath.Join(s.root, fileMetaDir, id+".json"), &rec); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return FileRecord{}, derrors.New(derrors.CodeNotFound, op, "file not found")
		}
		return FileRecord{}, derrors.Wrap(err, derrors.CodeInternal, op, "read meta")
	}
	return rec, nil
}

// ReadFileContent returns stored bytes for a file id.
func (s *DiskStore) ReadFileContent(id string) ([]byte, error) {
	if s == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "batch.ReadFileContent", "not configured")
	}
	id, err := checkID("batch.ReadFileContent", id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	contentPath := filepath.Join(s.root, fileContentDir, id)
	raw, err := os.ReadFile(contentPath) //nolint:gosec // G304: id passed checkID, so the path stays under the store root
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, derrors.New(derrors.CodeNotFound, "batch.ReadFileContent", "file not found")
		}
		return nil, derrors.Wrap(err, derrors.CodeInternal, "batch.ReadFileContent", "read content")
	}
	return raw, nil
}

// DeleteFile removes metadata and content when not referenced by an active batch.
func (s *DiskStore) DeleteFile(id string) error {
	if s == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "batch.DeleteFile", "not configured")
	}
	id, err := checkID("batch.DeleteFile", id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.readFileMeta("batch.DeleteFile", id); err != nil {
		return err
	}
	if inUse, err := s.fileReferencedByBatchLocked(id); err != nil {
		return err
	} else if inUse {
		return derrors.New(derrors.CodeConflict, "batch.DeleteFile", "file referenced by a batch")
	}
	return s.removeFileUnlocked(id)
}

// removeFileUnlocked deletes a file's metadata and content; the caller holds s.mu.
func (s *DiskStore) removeFileUnlocked(id string) error {
	for _, path := range []string{filepath.Join(s.root, fileMetaDir, id+".json"), filepath.Join(s.root, fileContentDir, id)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return derrors.Wrap(err, derrors.CodeInternal, "batch.removeFile", "remove").With("file_id", id)
		}
	}
	return nil
}

func (s *DiskStore) fileReferencedByBatchLocked(fileID string) (bool, error) {
	inUse := false
	err := s.eachBatchUnlocked("batch.fileReferencedByBatchLocked", func(meta Meta) bool {
		inUse = meta.InputFileID == fileID || meta.OutputFileID == fileID || meta.ErrorFileID == fileID
		return !inUse
	})
	// Fail closed: an unreadable batch may reference this file.
	return inUse, err
}

// eachBatchUnlocked calls fn with every stored batch until fn returns false. A batch
// that cannot be read stops the scan with an error. The caller holds s.mu.
func (s *DiskStore) eachBatchUnlocked(op string, fn func(Meta) bool) error {
	entries, err := os.ReadDir(filepath.Join(s.root, batchMetaDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return derrors.Wrap(err, derrors.CodeInternal, op, "readdir")
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		meta, err := s.readBatchMetaUnlocked(id)
		if err != nil {
			return derrors.Wrap(err, derrors.CodeInternal, op, "read batch meta").With("batch_id", id)
		}
		if !fn(meta) {
			return nil
		}
	}
	return nil
}

// SaveBatch writes batch metadata.
func (s *DiskStore) SaveBatch(meta *Meta) error {
	if s == nil || meta == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "batch.SaveBatch", "not configured")
	}
	if meta.ID == "" {
		meta.ID = newID("batch")
	}
	if meta.Object == "" {
		meta.Object = "batch"
	}
	if meta.CreatedAt == 0 {
		meta.CreatedAt = s.unixNow()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBatchMeta("batch.SaveBatch", meta)
}

// UpdateBatch replaces batch metadata.
func (s *DiskStore) UpdateBatch(meta Meta) error {
	if s == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "batch.UpdateBatch", "not configured")
	}
	if _, err := checkID("batch.UpdateBatch", meta.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBatchMeta("batch.UpdateBatch", &meta)
}

// writeBatchMeta stores meta; op names the caller in errors. The caller holds s.mu.
func (s *DiskStore) writeBatchMeta(op string, meta *Meta) error {
	if _, err := checkID(op, meta.ID); err != nil {
		return err
	}
	metaPath := filepath.Join(s.root, batchMetaDir, meta.ID+".json")
	raw, err := json.Marshal(meta)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "marshal")
	}
	return writeFileAtomic(metaPath, raw)
}

// GetBatch loads batch metadata.
func (s *DiskStore) GetBatch(id string) (Meta, error) {
	if s == nil {
		return Meta{}, derrors.New(derrors.CodeFailedPrecondition, "batch.GetBatch", "not configured")
	}
	id, err := checkID("batch.GetBatch", id)
	if err != nil {
		return Meta{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getBatchUnlocked("batch.GetBatch", id)
}

// getBatchUnlocked reads one batch, mapping a missing one to CodeNotFound; op names
// the caller. id must have passed checkID, and the caller holds s.mu.
func (s *DiskStore) getBatchUnlocked(op, id string) (Meta, error) {
	meta, err := s.readBatchMetaUnlocked(id)
	if errors.Is(err, fs.ErrNotExist) {
		return Meta{}, derrors.New(derrors.CodeNotFound, op, "batch not found")
	}
	if err != nil {
		return Meta{}, derrors.Wrap(err, derrors.CodeInternal, op, "read")
	}
	return meta, nil
}

// ListBatches returns all batch metadata (unordered).
func (s *DiskStore) ListBatches() ([]Meta, error) {
	if s == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "batch.ListBatches", "not configured")
	}
	// Read the directory under the lock, so a batch saved meanwhile is not missed.
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Meta{}
	if err := s.eachBatchUnlocked("batch.ListBatches", func(meta Meta) bool {
		out = append(out, meta)
		return true
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// readBatchMetaUnlocked reads a batch's metadata; a missing batch keeps
// fs.ErrNotExist in the chain. id must have passed checkID.
func (s *DiskStore) readBatchMetaUnlocked(id string) (Meta, error) {
	var meta Meta
	if err := readJSON(filepath.Join(s.root, batchMetaDir, id+".json"), &meta); err != nil {
		return Meta{}, err
	}
	return meta, nil
}

// readJSON decodes the JSON file at path into out. Every caller builds path from an
// id that passed checkID, so it stays under the store root.
func readJSON(path string, out any) error {
	raw, err := os.ReadFile(path) //nolint:gosec // G304: callers build path from an id that passed checkID
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func writeFileAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "batch.writeFileAtomic", "mkdir")
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "batch.writeFileAtomic", "temp")
	}
	tmpName := tmp.Name()
	// Cleanup errors join the cause, so a leftover .tmp-* file leaves a trace.
	if _, err := tmp.Write(content); err != nil {
		cleanup := errors.Join(tmp.Close(), os.Remove(tmpName))
		return derrors.Wrap(errors.Join(err, cleanup), derrors.CodeInternal, "batch.writeFileAtomic", "write")
	}
	if err := tmp.Close(); err != nil {
		return derrors.Wrap(errors.Join(err, os.Remove(tmpName)), derrors.CodeInternal, "batch.writeFileAtomic", "close")
	}
	if err := os.Rename(tmpName, path); err != nil {
		return derrors.Wrap(errors.Join(err, os.Remove(tmpName)), derrors.CodeInternal, "batch.writeFileAtomic", "rename")
	}
	return nil
}
