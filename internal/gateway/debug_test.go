package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestServeFailureDump(t *testing.T) {
	dir := t.TempDir()
	traceID := "2efd44e46ff342b49d070a2a3ba933e5"
	body := []byte(`{"trace_id":"` + traceID + `"}`)
	if err := os.WriteFile(filepath.Join(dir, traceID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	h := healthHandler{responder: testResponder, dumpDir: dir}
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

func TestServeFailureDumpListsStreamChunkDumps(t *testing.T) {
	dir := t.TempDir()
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "stream")
	defer span.End()
	traceID := span.SpanContext().TraceID().String()
	failure := `{"trace_id":"` + traceID + `"}`
	if err := os.WriteFile(filepath.Join(dir, traceID+".json"), []byte(failure), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx = withStreamChunkCounter(ctx, chunkDumpSink{dir: dir, log: testResponder.log, now: time.Now})
	dumpIllegalStreamChunk(ctx, 2, []byte(`{"choices":[]}`), nil)
	dumpIllegalStreamChunk(ctx, 10, []byte(`not-json`), nil)

	rec := httptest.NewRecorder()
	healthHandler{responder: testResponder, dumpDir: dir}.serveFailureDump(rec, httptest.NewRequest(http.MethodGet, "/debug/failures/"+traceID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got traceDumps
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if got.TraceID != traceID || string(got.Failure) != failure || len(got.StreamChunks) != 2 {
		t.Fatalf("the trace's failure and both chunk dumps must be listed: %s", rec.Body.String())
	}
	if !strings.Contains(got.StreamChunks[0].File, "000002") || !strings.Contains(got.StreamChunks[1].File, "000010") {
		t.Fatalf("chunk dumps must come in chunk order: %+v", got.StreamChunks)
	}
	if !strings.Contains(rec.Body.String(), `"openai":"not-json"`) {
		t.Fatalf("a chunk that is not JSON must still be dumped, as text: %s", rec.Body.String())
	}
}

func TestServeFailureDumpMissingTraceIsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	healthHandler{responder: testResponder, dumpDir: t.TempDir()}.serveFailureDump(rec, httptest.NewRequest(http.MethodGet, "/debug/failures/2efd44e46ff342b49d070a2a3ba933e5", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}
