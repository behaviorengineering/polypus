package gateway

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/openaicompat"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// testResponder answers for the handler helpers under test, logging nowhere.
var testResponder = responder{log: slog.New(slog.DiscardHandler)}

// testCompat is the OpenAI-compatible client for helpers that list models.
var testCompat = openaicompat.Client{HTTP: http.DefaultClient, Policies: outbound.NewPolicies()}

// testRequest stands in for the request a handler helper answers.
var testRequest = httptest.NewRequest(http.MethodGet, "/test", nil)

func TestRequestFailureLogsToTheInjectedLogger(t *testing.T) {
	var buf bytes.Buffer
	s := responder{log: slog.New(slog.NewTextHandler(&buf, nil))}
	rec := httptest.NewRecorder()
	s.writeHandlerError(rec, testRequest, derrors.New(derrors.CodeInternal, "gateway.test", "boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "request failed") || !strings.Contains(buf.String(), "boom") {
		t.Fatalf("the failure must reach the gateway's logger: %q", buf.String())
	}
}

func TestRequestFailureLogNamesTheRouteAndTrace(t *testing.T) {
	var buf bytes.Buffer
	s := responder{log: slog.New(slog.NewTextHandler(&buf, nil))}
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "request")
	defer span.End()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, pathChatCompletions, nil)
	s.writeHandlerError(httptest.NewRecorder(), r, derrors.New(derrors.CodeInternal, "gateway.test", "boom"))
	for _, want := range []string{"method=POST", "path=" + pathChatCompletions, "trace_id=" + span.SpanContext().TraceID().String()} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("a 5xx log line must carry %q: %q", want, buf.String())
		}
	}
}
