package gateway

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// testShared carries the logger the response writers need; tests that only write
// responses use it in place of a full gateway.
var testShared = &shared{log: slog.New(slog.DiscardHandler)}

func TestRequestFailureLogsToTheInjectedLogger(t *testing.T) {
	var buf bytes.Buffer
	s := &shared{log: slog.New(slog.NewTextHandler(&buf, nil))}
	rec := httptest.NewRecorder()
	s.writeHandlerError(rec, derrors.New(derrors.CodeInternal, "gateway.test", "boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "request failed") || !strings.Contains(buf.String(), "boom") {
		t.Fatalf("the failure must reach the gateway's logger: %q", buf.String())
	}
}
