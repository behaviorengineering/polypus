package gateway

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFileAndBatchIDsCannotLeaveTheStore(t *testing.T) {
	h, _ := newOpenRouterBatchGateway(t)
	for _, tc := range []struct {
		method, target, body string
	}{
		{http.MethodGet, "/v1/files/..%2F..%2F..%2Fsecret/content", ""},
		{http.MethodGet, "/v1/files/..%2F..%2Fsecret", ""},
		{http.MethodDelete, "/v1/files/..%2F..%2Fsecret", ""},
		{http.MethodGet, "/v1/batches/..%2F..%2Fsecret", ""},
		{http.MethodPost, "/v1/batches", `{"input_file_id":"../../secret","endpoint":"/v1/chat/completions","completion_window":"24h"}`},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status %d, want 400: %s", tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}
}

func TestOversizedRequestBodiesGet413(t *testing.T) {
	h, _ := newOpenRouterBatchGateway(t)
	speech := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(strings.Repeat("a", speechMaxBody+1)))
	speech.Header.Set("Content-Type", "application/json")
	for _, req := range []*http.Request{
		speech,
		multipartUpload(t, "/v1/audio/transcriptions", transcriptionMaxBody+1),
		multipartUpload(t, "/v1/files", batchMaxInputBytes+1<<20+1),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("POST %s: status %d, want 413: %.200s", req.URL.Path, rec.Code, rec.Body.String())
		}
	}
}

// multipartUpload builds a POST with one "file" part of size bytes.
func multipartUpload(t *testing.T, target string, size int) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("purpose", "batch"); err != nil {
		t.Fatal(err)
	}
	part, err := mw.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("a"), size)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}
