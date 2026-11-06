package observability

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func endDialSpanForTest(t *testing.T, err error, f DialFailure) tracetest.SpanStub {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	_, span := tp.Tracer("test").Start(context.Background(), "dial")
	EndDialSpan(span, err, f)
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("want one span, got %d", len(spans))
	}
	return spans[0]
}

func TestEndDialSpanRecordsEveryDialFailureField(t *testing.T) {
	sp := endDialSpanForTest(t, errors.New("dial refused"), DialFailure{
		Layer:        "polypus_breaker",
		Upstream:     "cf_local",
		BreakerState: "half-open",
		HTTPStatus:   503,
	})
	if sp.Status.Code != codes.Error {
		t.Fatalf("status %v", sp.Status.Code)
	}
	for key, want := range map[string]string{
		"polypus.failure.layer":         "polypus_breaker",
		"polypus.failure.upstream":      "cf_local",
		"polypus.circuit_breaker.state": "half-open",
	} {
		if got, _ := attrString(sp, key); got != want {
			t.Fatalf("%s=%q want %q", key, got, want)
		}
	}
	if got := attrInt(sp, "polypus.failure.http_status"); got != 503 {
		t.Fatalf("http_status=%d", got)
	}
	var ev map[string]string
	for _, e := range sp.Events {
		if e.Name != "circuit_breaker" {
			continue
		}
		ev = map[string]string{}
		for _, a := range e.Attributes {
			ev[string(a.Key)] = a.Value.AsString()
		}
	}
	if ev["upstream"] != "cf_local" || ev["state"] != "half-open" {
		t.Fatalf("event attrs %v", ev)
	}
}

func TestEndDialSpanWithoutBreakerStateAddsNoEvent(t *testing.T) {
	sp := endDialSpanForTest(t, errors.New("leaf 502"), DialFailure{Layer: "leaf", Upstream: "lm_studio"})
	for _, e := range sp.Events {
		if e.Name == "circuit_breaker" {
			t.Fatal("a failure with no breaker state must not add a circuit_breaker event")
		}
	}
	if _, ok := attrString(sp, "polypus.circuit_breaker.state"); ok {
		t.Fatal("unexpected breaker state attribute")
	}
	if attrInt(sp, "polypus.failure.http_status") != 0 {
		t.Fatal("unexpected http status attribute")
	}
}

func TestEndDialSpanSuccessIgnoresLabels(t *testing.T) {
	sp := endDialSpanForTest(t, nil, DialFailure{Layer: "leaf", Upstream: "x"})
	if sp.Status.Code != codes.Ok {
		t.Fatalf("status %v", sp.Status.Code)
	}
	if _, ok := attrString(sp, "polypus.failure.layer"); ok {
		t.Fatal("a successful dial must carry no failure labels")
	}
}

// TestObservabilityImportsNoGatewayPackages keeps observability a separate domain:
// the gateway classifies its own failures and passes the labels in.
func TestObservabilityImportsNoGatewayPackages(t *testing.T) {
	const forbidden = "github.com/behaviorengineering/polypus/internal/gateway"
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		src, err := os.ReadFile(name) //nolint:gosec // G304: this package's own source files
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == forbidden || strings.HasPrefix(path, forbidden+"/") {
				t.Errorf("%s imports %s", name, path)
			}
		}
	}
}

func attrInt(sp tracetest.SpanStub, key string) int64 {
	for _, attr := range sp.Attributes {
		if string(attr.Key) == key {
			return attr.Value.AsInt64()
		}
	}
	return 0
}
