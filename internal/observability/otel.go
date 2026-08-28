package observability

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const instrumentationName = "github.com/behaviorengineering/polypus"

var skipHTTPPaths = append([]string(nil), defaultSkipPaths...)

// Init installs the global tracer provider, W3C propagator, and optional dump processor.
func Init(cfg Config) (func(context.Context) error, error) {
	skip := cfg.SkipPaths
	if skip == nil {
		skip = defaultSkipPaths
	}
	skipHTTPPaths = append([]string(nil), skip...)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if !cfg.Enabled {
		otel.SetTracerProvider(noop.NewTracerProvider())
		return func(context.Context) error { return nil }, nil
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = defaultServiceName
	}
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("observability: resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}
	if cfg.DumpDir != "" {
		opts = append(opts, sdktrace.WithSpanProcessor(newFailureDumpProcessor(
			cfg.DumpDir, cfg.DumpMaxAgeH, cfg.DumpMaxFiles,
		)))
	}
	if cfg.OTLPEndpoint != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		exporter, expErr := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if expErr != nil {
			fmt.Fprintf(os.Stderr, "polypus otel: OTLP exporter failed (%v); dump-only tracing continues\n", expErr)
		} else {
			opts = append(opts, sdktrace.WithBatcher(
				exporter,
				sdktrace.WithBatchTimeout(2*time.Second),
				sdktrace.WithMaxExportBatchSize(512),
			))
		}
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// Tracer returns the Polypus tracer.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// WrapHandler extracts incoming W3C context and creates SERVER spans for each request.
// Paths in SkipPaths (default /health) are not traced.
func WrapHandler(h http.Handler) http.Handler {
	skip := append([]string(nil), skipHTTPPaths...)
	return wrapHandler(h, skip)
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
