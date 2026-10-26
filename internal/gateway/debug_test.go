package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServeFailureDump(t *testing.T) {
	dir := t.TempDir()
	traceID := "2efd44e46ff342b49d070a2a3ba933e5"
	body := []byte(`{"trace_id":"` + traceID + `"}`)
	if err := os.WriteFile(filepath.Join(dir, traceID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	sh := *testShared
	sh.dumpDir = dir
	h := healthHandler{shared: &sh}
	req := httptest.NewRequest(http.MethodGet, "/debug/failures/"+traceID, nil)
	rec := httptest.NewRecorder()
	h.serveFailureDump(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(body) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}
