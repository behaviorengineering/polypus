package batch

import (
	"encoding/json"
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
	id = strings.TrimSpace(id)
	if id == "" {
		return FileRecord{}, derrors.New(derrors.CodeInvalid, "batch.GetFile", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readFileMeta(id)
}

func (s *DiskStore) readFileMeta(id string) (FileRecord, error) {
	metaPath := filepath.Join(s.root, fileMetaDir, id+".json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return FileRecord{}, derrors.New(derrors.CodeNotFound, "batch.GetFile", "file not found")
		}
		return FileRecord{}, derrors.Wrap(err, derrors.CodeInternal, "batch.GetFile", "read meta")
	}
	var rec FileRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return FileRecord{}, derrors.Wrap(err, derrors.CodeInternal, "batch.GetFile", "parse meta")
	}
	return rec, nil
}

// ReadFileContent returns stored bytes for a file id.
func (s *DiskStore) ReadFileContent(id string) ([]byte, error) {
	if s == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "batch.ReadFileContent", "not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, derrors.New(derrors.CodeInvalid, "batch.ReadFileContent", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	contentPath := filepath.Join(s.root, fileContentDir, id)
	raw, err := os.ReadFile(contentPath)
	if err != nil {
		if os.IsNotExist(err) {
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
	id = strings.TrimSpace(id)
	if id == "" {
		return derrors.New(derrors.CodeInvalid, "batch.DeleteFile", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if inUse, err := s.fileReferencedByBatchLocked(id); err != nil {
		return err
	} else if inUse {
		return derrors.New(derrors.CodeConflict, "batch.DeleteFile", "file referenced by a batch")
	}
	_ = os.Remove(filepath.Join(s.root, fileMetaDir, id+".json"))
	_ = os.Remove(filepath.Join(s.root, fileContentDir, id))
	return nil
}

func (s *DiskStore) fileReferencedByBatchLocked(fileID string) (bool, error) {
	dir := filepath.Join(s.root, batchMetaDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, derrors.Wrap(err, derrors.CodeInternal, "batch.fileReferencedByBatchLocked", "readdir")
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		bid := e.Name()[:len(e.Name())-len(".json")]
		meta, err := s.readBatchMetaUnlocked(bid)
		if err != nil {
			continue
		}
		if meta.InputFileID == fileID || meta.OutputFileID == fileID || meta.ErrorFileID == fileID {
			return true, nil
		}
	}
	return false, nil
}

// SaveBatch writes batch metadata.
func (s *DiskStore) SaveBatch(meta *BatchMeta) error {
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
	return s.writeBatchMeta(meta)
}

// UpdateBatch replaces batch metadata.
func (s *DiskStore) UpdateBatch(meta BatchMeta) error {
	if s == nil {
		return derrors.New(derrors.CodeFailedPrecondition, "batch.UpdateBatch", "not configured")
	}
	if strings.TrimSpace(meta.ID) == "" {
		return derrors.New(derrors.CodeInvalid, "batch.UpdateBatch", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeBatchMeta(&meta)
}

func (s *DiskStore) writeBatchMeta(meta *BatchMeta) error {
	metaPath := filepath.Join(s.root, batchMetaDir, meta.ID+".json")
	raw, err := json.Marshal(meta)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "batch.SaveBatch", "marshal")
	}
	return writeFileAtomic(metaPath, raw)
}

// GetBatch loads batch metadata.
func (s *DiskStore) GetBatch(id string) (BatchMeta, error) {
	if s == nil {
		return BatchMeta{}, derrors.New(derrors.CodeFailedPrecondition, "batch.GetBatch", "not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return BatchMeta{}, derrors.New(derrors.CodeInvalid, "batch.GetBatch", "id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	metaPath := filepath.Join(s.root, batchMetaDir, id+".json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return BatchMeta{}, derrors.New(derrors.CodeNotFound, "batch.GetBatch", "batch not found")
		}
		return BatchMeta{}, derrors.Wrap(err, derrors.CodeInternal, "batch.GetBatch", "read")
	}
	var meta BatchMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return BatchMeta{}, derrors.Wrap(err, derrors.CodeInternal, "batch.GetBatch", "parse")
	}
	return meta, nil
}

// ListBatches returns all batch metadata (unordered).
func (s *DiskStore) ListBatches() ([]BatchMeta, error) {
	if s == nil {
		return nil, derrors.New(derrors.CodeFailedPrecondition, "batch.ListBatches", "not configured")
	}
	dir := filepath.Join(s.root, batchMetaDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, derrors.Wrap(err, derrors.CodeInternal, "batch.ListBatches", "readdir")
	}
	out := make([]BatchMeta, 0, len(entries))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := e.Name()[:len(e.Name())-len(".json")]
		meta, err := s.readBatchMetaUnlocked(id)
		if err != nil {
			continue
		}
		out = append(out, meta)
	}
	return out, nil
}

func (s *DiskStore) readBatchMetaUnlocked(id string) (BatchMeta, error) {
	metaPath := filepath.Join(s.root, batchMetaDir, id+".json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return BatchMeta{}, err
	}
	var meta BatchMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return BatchMeta{}, err
	}
	return meta, nil
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
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "batch.writeFileAtomic", "write")
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "batch.writeFileAtomic", "close")
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "batch.writeFileAtomic", "rename")
	}
	return nil
}
