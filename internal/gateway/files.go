package gateway

import (
	"io"
	"net/http"
	"strings"

	"github.com/behaviorengineering/polypus/internal/batch"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

const batchMaxInputBytes = 10 << 20

type filesHandler struct{ *shared }

func (h filesHandler) serveFiles(w http.ResponseWriter, r *http.Request) {
	if h.batchStore == nil {
		h.writeHandlerError(w, derrors.New(derrors.CodeNotReady, "gateway.serveFiles", "batch store not configured"))
		return
	}
	if r.URL.Path == pathFiles {
		if r.Method == http.MethodPost {
			h.serveFileUpload(w, r)
			return
		}
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	path = strings.Trim(path, "/")
	if path == "" {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(path, "/content") {
		id := strings.TrimSuffix(path, "/content")
		id = strings.Trim(id, "/")
		if r.Method == http.MethodGet {
			h.serveFileContent(w, r, id)
			return
		}
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.serveFileRetrieve(w, r, path)
	case http.MethodDelete:
		h.serveFileDelete(w, r, path)
	default:
		http.NotFound(w, r)
	}
}

func (h filesHandler) serveFileUpload(w http.ResponseWriter, r *http.Request) {
	// The batch input cap plus multipart overhead; past it the upload gets 413.
	r.Body = http.MaxBytesReader(w, r.Body, batchMaxInputBytes+1<<20)
	if err := r.ParseMultipartForm(batchMaxInputBytes); err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveFileUpload", "multipart"))
		return
	}
	purpose := strings.TrimSpace(r.FormValue("purpose"))
	if purpose != string(batch.FilePurposeBatch) {
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveFileUpload", "purpose must be batch"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveFileUpload", "file required"))
		return
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, batchMaxInputBytes+1))
	if err != nil {
		h.writeHandlerError(w, derrors.Wrap(err, derrors.CodeInvalid, "gateway.serveFileUpload", "read file"))
		return
	}
	if len(content) > batchMaxInputBytes {
		h.writeHandlerError(w, derrors.New(derrors.CodeInvalid, "gateway.serveFileUpload", "file exceeds 10MB batch limit"))
		return
	}
	filename := "batch.jsonl"
	if header != nil && strings.TrimSpace(header.Filename) != "" {
		filename = header.Filename
	}
	rec := &batch.FileRecord{
		Filename: filename,
		Purpose:  batch.FilePurposeBatch,
	}
	if err := h.batchStore.SaveFile(rec, content); err != nil {
		h.writeHandlerError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, rec)
}

func (h filesHandler) serveFileRetrieve(w http.ResponseWriter, r *http.Request, id string) {
	rec, err := h.batchStore.GetFile(id)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, rec)
}

func (h filesHandler) serveFileContent(w http.ResponseWriter, r *http.Request, id string) {
	content, err := h.batchStore.ReadFileContent(id)
	if err != nil {
		h.writeHandlerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/jsonl")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content) //nolint:gosec // G705: stored JSONL served as application/jsonl with nosniff
}

func (h filesHandler) serveFileDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.batchStore.DeleteFile(id); err != nil {
		h.writeHandlerError(w, err)
		return
	}
	var deleted struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Deleted bool   `json:"deleted"`
	}
	deleted.ID = id
	deleted.Object = "file"
	deleted.Deleted = true
	h.writeJSON(w, http.StatusOK, deleted)
}
