package observability

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/behaviorengineering/olly"
	"github.com/behaviorengineering/olly/dump"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/behaviorengineering/polypus"

// skipHTTPPaths holds the active SERVER skip list (set by Init; read by WrapHandler).
var skipHTTPPaths atomic.Value // []string

func init() {
	skipHTTPPaths.Store(append([]string(nil), defaultSkipPaths...))
}

// Init installs the global tracer provider, W3C propagator, and optional dump processor via olly.
func Init(cfg Config) (func(context.Context) error, error) {
	skip := cfg.SkipPaths
	if skip == nil {
		skip = defaultSkipPaths
	}
	skipHTTPPaths.Store(append([]string(nil), skip...))

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = defaultServiceName
	}

	ollyCfg := olly.Config{
		Enabled:          cfg.Enabled,
		ServiceName:      serviceName,
		OTLPEndpoint:     cfg.OTLPEndpoint,
		AllowOTLPFailure: true,
		BatchTimeout:     2 * time.Second,
		OTLPTimeout:      5 * time.Second,
		Dump: dump.Config{
			Dir:              cfg.DumpDir,
			MaxAgeHours:      cfg.DumpMaxAgeH,
			MaxFiles:         cfg.DumpMaxFiles,
			RedactAttribute:  dumpRedactAttribute,
			RedactStatusText: redactURLsInText,
			Diagnostics:      stderrDiag{},
		},
	}
	if !cfg.Enabled {
		ollyCfg.OTLPEndpoint = ""
		ollyCfg.Dump.Dir = ""
	}

	shutdown, err := olly.Init(ollyCfg)
	if err != nil {
		return nil, err
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return shutdown, nil
}

type stderrDiag struct{}

func (stderrDiag) Printf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func dumpRedactAttribute(key string, value any) any {
	str, ok := value.(string)
	if !ok {
		return value
	}
	return sanitizeAttrValue(key, str)
}

// Tracer returns the Polypus tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// WrapHandler extracts incoming W3C context and creates SERVER spans for each request.
// Paths in SkipPaths (default /health) are not traced.
func WrapHandler(h http.Handler) http.Handler {
	skip, _ := skipHTTPPaths.Load().([]string)
	if skip == nil {
		skip = defaultSkipPaths
	}
	return wrapHandler(h, append([]string(nil), skip...))
}

func wrapHandler(h http.Handler, skip []string) http.Handler {
	return otelhttp.NewHandler(h, "polypus",
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !pathIsSkipped(r.URL.Path, skip)
		}),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return httpServerSpanName(r)
		}),
		otelhttp.WithSpanOptions(trace.WithAttributes(
			attribute.String("openinference.span.kind", "CHAIN"),
			attribute.String("http.io", "server"),
		)),
	)
}

func httpServerSpanName(r *http.Request) string {
	if r == nil || r.URL == nil {
		return defaultServiceName + " SERVER HTTP"
	}
	return defaultServiceName + " SERVER " + r.Method + " " + r.URL.Path
}

func httpClientSpanName(r *http.Request) string {
	if r == nil || r.URL == nil {
		return defaultServiceName + " CLIENT HTTP"
	}
	return defaultServiceName + " CLIENT " + r.Method + " " + r.URL.Host + r.URL.Path
}

// WrapTransport injects W3C context on outbound HTTP (backend arms).
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if _, ok := base.(*otelhttp.Transport); ok {
		return base
	}
	return otelhttp.NewTransport(withErrorDetail(base),
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			return httpClientSpanName(r)
		}),
		otelhttp.WithSpanOptions(trace.WithAttributes(
			attribute.String("openinference.span.kind", "CHAIN"),
			attribute.String("http.io", "client"),
		)),
	)
}

// StartRouterSpan starts an OpenInference span for a composed router hop via Switchyard.
func StartRouterSpan(ctx context.Context, operation, model, routerName, switchyardURL string) (context.Context, trace.Span) {
	ctx, span := Tracer().Start(ctx, operation, trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		attribute.String("openinference.span.kind", "LLM"),
		attribute.String("llm.model_name", model),
		attribute.String("llm.provider", "polypus"),
		attribute.String("polypus.backend_id", "switchyard"),
		attribute.String("polypus.backend_url", switchyardURL),
		attribute.String("polypus.router_name", routerName),
	)
	return ctx, span
}

// StartLLMSpan starts an OpenInference LLM span for a routed inference call.
func StartLLMSpan(ctx context.Context, operation, model, backendID, backendURL, downstream string) (context.Context, trace.Span) {
	ctx, span := Tracer().Start(ctx, operation, trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(
		attribute.String("openinference.span.kind", "LLM"),
		attribute.String("llm.model_name", model),
		attribute.String("llm.provider", "polypus"),
		attribute.String("polypus.backend_id", backendID),
		attribute.String("polypus.backend_url", backendURL),
		attribute.String("polypus.downstream_model", downstream),
	)
	return ctx, span
}

// EndSpan records err on the span and ends it.
func EndSpan(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		annotateSpanError(span, err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	span.End()
}
