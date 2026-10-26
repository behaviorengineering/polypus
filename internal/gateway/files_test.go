package gateway

import (
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
