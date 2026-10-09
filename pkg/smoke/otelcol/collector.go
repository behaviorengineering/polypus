package otelcol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/behaviorengineering/polypus/internal/outbound"
)

// CollectorOptions configures OTLP fan-out smoke against otelcol, Phoenix, and HyperDX.
type CollectorOptions struct {
	OTLPEndpoint     string
	PhoenixBaseURL   string
	PhoenixProject   string
	PhoenixAPIKey    string
	HyperDXBaseURL   string
	HyperDXAPIKey    string
	HyperDXContainer string
	PollInterval     time.Duration
	AllowHyperDXCH   bool
}

// DefaultCollectorPollInterval is the delay between backend polls.
const DefaultCollectorPollInterval = 2 * time.Second

// DefaultCollectorSmokeTimeout is the collector smoke budget.
const DefaultCollectorSmokeTimeout = 90 * time.Second

var errHyperDXAPIUnavailable = errors.New("hyperdx: v2 search API unavailable")

// RunCollector exports two synthetic traces through otelcol and verifies Phoenix / HyperDX routing.
func RunCollector(ctx context.Context, opts CollectorOptions) error {
	if ctx == nil {
		return fmt.Errorf("collector smoke: context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("collector smoke: missing deadline")
	}
	opts = opts.withCollectorDefaults()

	fmt.Printf("collector smoke: otlp=%s phoenix=%s hyperdx=%s\n",
		opts.OTLPEndpoint, opts.PhoenixBaseURL, opts.HyperDXBaseURL)

	genAITrace, err := exportCollectorSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-genai",
		attribute.String("gen_ai.system", "polypus-local-smoke"),
		attribute.String("smoke.probe", "gen_ai"),
	)
	if err != nil {
		return fmt.Errorf("collector smoke: export gen_ai span: %w", err)
	}
	plainTrace, err := exportCollectorSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-plain",
		attribute.String("smoke.probe", "plain"),
	)
	if err != nil {
		return fmt.Errorf("collector smoke: export plain span: %w", err)
	}
	genAITrace, err = normalizeTraceIDHex(genAITrace)
	if err != nil {
		return fmt.Errorf("collector smoke: gen_ai trace id: %w", err)
	}
	plainTrace, err = normalizeTraceIDHex(plainTrace)
	if err != nil {
		return fmt.Errorf("collector smoke: plain trace id: %w", err)
	}
	fmt.Printf("collector smoke: gen_ai trace_id=%s plain trace_id=%s\n", genAITrace, plainTrace)
	fmt.Println("collector smoke: otlp export ok")

	deadline, _ := ctx.Deadline()
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("collector smoke: %w (last probe: %v)", err, lastErr)
			}
			return fmt.Errorf("collector smoke: %w", err)
		}

		genPhoenix, errGenPhoenix := phoenixSpanCount(ctx, opts, genAITrace)
		plainPhoenix, errPlainPhoenix := phoenixSpanCount(ctx, opts, plainTrace)
		genHyper, errGenHyper := hyperdxTraceCount(ctx, opts, genAITrace)
		plainHyper, errPlainHyper := hyperdxTraceCount(ctx, opts, plainTrace)

		fmt.Printf("collector smoke: probe counts gen_ai phoenix=%d hyperdx=%d plain phoenix=%d hyperdx=%d\n",
			genPhoenix, genHyper, plainPhoenix, plainHyper)

		okGenPhoenix := errGenPhoenix == nil && genPhoenix > 0
		okPlainAbsentPhoenix := errPlainPhoenix == nil && plainPhoenix == 0
		okGenHyper := errGenHyper == nil && genHyper > 0
		okPlainHyper := errPlainHyper == nil && plainHyper > 0

		if okGenPhoenix && okPlainAbsentPhoenix && okGenHyper && okPlainHyper {
			fmt.Println("collector smoke: routing ok (gen_ai -> Phoenix+HyperDX, plain -> HyperDX only)")
			return nil
		}

		lastErr = joinProbeErrors(genPhoenix, plainPhoenix, genHyper, plainHyper,
			errGenPhoenix, errPlainPhoenix, errGenHyper, errPlainHyper,
			okGenPhoenix, okPlainAbsentPhoenix, okGenHyper, okPlainHyper)

		if time.Now().Add(opts.PollInterval).After(deadline) {
			return fmt.Errorf("collector smoke: timed out: %v", lastErr)
		}
		if err := sleepContext(ctx, opts.PollInterval); err != nil {
			return err
		}
	}
}

func (o CollectorOptions) withCollectorDefaults() CollectorOptions {
	out := o
	if strings.TrimSpace(out.OTLPEndpoint) == "" {
		out.OTLPEndpoint = envOr("POLYPUS_OTLP_ENDPOINT", envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:4317"))
	}
	if strings.TrimSpace(out.PhoenixBaseURL) == "" {
		out.PhoenixBaseURL = envOr("PHOENIX_BASE_URL", "http://127.0.0.1:6006")
	}
	if strings.TrimSpace(out.PhoenixProject) == "" {
		out.PhoenixProject = envOr("PHOENIX_PROJECT", "default")
	}
	if strings.TrimSpace(out.PhoenixAPIKey) == "" {
		out.PhoenixAPIKey = strings.TrimSpace(os.Getenv("PHOENIX_API_KEY"))
	}
	if strings.TrimSpace(out.HyperDXBaseURL) == "" {
		out.HyperDXBaseURL = envOr("HYPERDX_BASE_URL", "http://127.0.0.1:8080")
	}
	if strings.TrimSpace(out.HyperDXAPIKey) == "" {
		out.HyperDXAPIKey = strings.TrimSpace(os.Getenv("HYPERDX_API_KEY"))
	}
	if strings.TrimSpace(out.HyperDXContainer) == "" {
		out.HyperDXContainer = envOr("POLYPUS_HYPERDX_CONTAINER", "polypus-hyperdx")
	}
	if out.PollInterval <= 0 {
		out.PollInterval = DefaultCollectorPollInterval
	}
	out.AllowHyperDXCH = true
	switch strings.ToLower(strings.TrimSpace(os.Getenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK"))) {
	case "0", "false", "no":
		out.AllowHyperDXCH = false
	}
	return out
}

func exportCollectorSpan(ctx context.Context, endpoint, spanName string, attrs ...attribute.KeyValue) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("missing OTLP endpoint")
	}

	var exp sdktrace.SpanExporter
	if useGRPCOTLPEndpoint(endpoint) {
		grpcEndpoint, insecure, err := normalizeGRPCEndpoint(endpoint)
		if err != nil {
			return "", fmt.Errorf("collector smoke: otlp grpc endpoint: %w", err)
		}
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(grpcEndpoint)}
		if insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		exp, err = otlptracegrpc.New(ctx, opts...)
		if err != nil {
			return "", fmt.Errorf("collector smoke: otlp grpc exporter: %w", err)
		}
	} else {
		httpURL, err := normalizeHTTPEndpoint(endpoint)
		if err != nil {
			return "", fmt.Errorf("collector smoke: otlp http endpoint: %w", err)
		}
		exp, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(httpURL))
		if err != nil {
			return "", fmt.Errorf("collector smoke: otlp http exporter: %w", err)
		}
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("polypus-local-collector-smoke"),
		),
	)
	if err != nil {
		return "", fmt.Errorf("collector smoke: otlp resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(exp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()

	tracer := tp.Tracer("polypus-local-collector-smoke")
	ctx, span := tracer.Start(ctx, spanName, trace.WithNewRoot())
	span.SetAttributes(attrs...)
	traceID := span.SpanContext().TraceID().String()
	span.End()
	if err := tp.ForceFlush(ctx); err != nil {
		return "", fmt.Errorf("collector smoke: otlp flush: %w", err)
	}
	return traceID, nil
}

func useGRPCOTLPEndpoint(endpoint string) bool {
	lower := strings.ToLower(endpoint)
	if strings.HasPrefix(lower, "grpc://") {
		return true
	}
	if strings.Contains(lower, ":4317") && !strings.Contains(lower, ":4318") {
		return true
	}
	return false
}

func normalizeGRPCEndpoint(endpoint string) (host string, insecure bool, err error) {
	raw := strings.TrimSpace(endpoint)
	insecure = true
	endpoint = strings.TrimPrefix(raw, "grpc://")
	if strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimPrefix(endpoint, "https://")
		insecure = false
	} else if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
		insecure = true
	}
	if idx := strings.Index(endpoint, "/"); idx >= 0 {
		endpoint = endpoint[:idx]
	}
	if endpoint == "" {
		return "", false, fmt.Errorf("invalid gRPC OTLP endpoint")
	}
	return endpoint, insecure, nil
}

func normalizeHTTPEndpoint(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/traces"
	}
	return u.String(), nil
}

func phoenixSpanCount(ctx context.Context, opts CollectorOptions, traceID string) (int, error) {
	traceID, err := normalizeTraceIDHex(traceID)
	if err != nil {
		return 0, err
	}
	base := strings.TrimRight(opts.PhoenixBaseURL, "/")
	project := url.PathEscape(opts.PhoenixProject)
	u := fmt.Sprintf("%s/v1/projects/%s/spans?trace_id=%s&limit=5", base, project, url.QueryEscape(traceID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix request: %w", err)
	}
	if opts.PhoenixAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.PhoenixAPIKey)
	}

	resp, err := smokeHTTPDo(ctx, http.DefaultClient, req)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix: %w", err)
	}
	body, err := readLimitedBody(resp, 1<<20)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: phoenix: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return 0, fmt.Errorf("phoenix: %s returned 404 (check PHOENIX_PROJECT)", u)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("phoenix: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, fmt.Errorf("phoenix: decode spans: %w", err)
	}
	return len(parsed.Data), nil
}

type hyperdxSource struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func hyperdxTraceCount(ctx context.Context, opts CollectorOptions, traceID string) (int, error) {
	n, err := hyperdxSearchV2(ctx, opts, traceID)
	if err == nil {
		return n, nil
	}
	if !errors.Is(err, errHyperDXAPIUnavailable) || !opts.AllowHyperDXCH {
		return 0, err
	}
	return hyperdxClickhouseDocker(ctx, opts.HyperDXContainer, traceID)
}

type hyperdxSearchPayload struct {
	SourceID      string `json:"sourceId"`
	StartTime     string `json:"startTime"`
	EndTime       string `json:"endTime"`
	Where         string `json:"where"`
	WhereLanguage string `json:"whereLanguage"`
	Select        string `json:"select"`
	MaxResults    int    `json:"maxResults"`
}

func hyperdxSearchV2(ctx context.Context, opts CollectorOptions, traceID string) (int, error) {
	traceID, err := normalizeTraceIDHex(traceID)
	if err != nil {
		return 0, err
	}
	base := strings.TrimRight(opts.HyperDXBaseURL, "/")
	sourcesURL := base + "/api/v2/sources"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourcesURL, nil)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: hyperdx sources request: %w", err)
	}
	if opts.HyperDXAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.HyperDXAPIKey)
	}
	resp, err := smokeHTTPDo(ctx, http.DefaultClient, req)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: hyperdx sources: %w", err)
	}
	body, err := readLimitedBody(resp, 1<<20)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: hyperdx sources: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound ||
		(resp.StatusCode == http.StatusUnauthorized && opts.HyperDXAPIKey == "") {
		return 0, errHyperDXAPIUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("hyperdx: sources: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var sources []hyperdxSource
	if err := json.Unmarshal(body, &sources); err != nil {
		var wrapped struct {
			Data []hyperdxSource `json:"data"`
		}
		if err2 := json.Unmarshal(body, &wrapped); err2 != nil {
			return 0, fmt.Errorf("hyperdx: decode sources: %w", err)
		}
		sources = wrapped.Data
	}
	sourceID := pickHyperDXTraceSource(sources)
	if sourceID == "" {
		return 0, fmt.Errorf("hyperdx: no trace source in /api/v2/sources")
	}

	now := time.Now().UTC()
	payload := hyperdxSearchPayload{
		SourceID:      sourceID,
		StartTime:     now.Add(-15 * time.Minute).Format(time.RFC3339),
		EndTime:       now.Add(2 * time.Minute).Format(time.RFC3339),
		Where:         fmt.Sprintf("TraceId = '%s'", traceID),
		WhereLanguage: "sql",
		Select:        "TraceId",
		MaxResults:    10,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: hyperdx search payload: %w", err)
	}
	searchURL := base + "/api/v2/search"
	sreq, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, bytes.NewReader(buf))
	if err != nil {
		return 0, fmt.Errorf("collector smoke: hyperdx search request: %w", err)
	}
	sreq.Header.Set("Content-Type", "application/json")
	if opts.HyperDXAPIKey != "" {
		sreq.Header.Set("Authorization", "Bearer "+opts.HyperDXAPIKey)
	}
	sresp, err := smokeHTTPDo(ctx, http.DefaultClient, sreq)
	if err != nil {
		return 0, fmt.Errorf("collector smoke: hyperdx search: %w", err)
	}
	sbody, err := readLimitedBody(sresp, 1<<20)
	if err != nil {
		return 0, err
	}
	if sresp.StatusCode == http.StatusNotFound {
		return 0, errHyperDXAPIUnavailable
	}
	if sresp.StatusCode < 200 || sresp.StatusCode >= 300 {
		return 0, fmt.Errorf("hyperdx: search: %s: %s", sresp.Status, strings.TrimSpace(string(sbody)))
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(sbody, &rows); err != nil {
		var wrapped struct {
			Data []json.RawMessage `json:"data"`
		}
		if err2 := json.Unmarshal(sbody, &wrapped); err2 != nil {
			return 0, fmt.Errorf("hyperdx: decode search: %w", err)
		}
		rows = wrapped.Data
	}
	return len(rows), nil
}

func pickHyperDXTraceSource(sources []hyperdxSource) string {
	for _, s := range sources {
		k := strings.ToLower(s.Kind)
		n := strings.ToLower(s.Name)
		if strings.Contains(k, "trace") || strings.Contains(n, "trace") {
			return s.ID
		}
	}
	if len(sources) > 0 {
		return sources[0].ID
	}
	return ""
}

func hyperdxClickhouseDocker(ctx context.Context, container, traceID string) (int, error) {
	traceID, err := normalizeTraceIDHex(traceID)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(container) == "" {
		return 0, fmt.Errorf("hyperdx: clickhouse fallback: missing container name")
	}
	query := fmt.Sprintf("SELECT count() FROM default.otel_traces WHERE TraceId = '%s'", traceID)
	var out []byte
	err = outbound.Run(ctx, outbound.DepDockerDaemon, func() error {
		cmd := exec.CommandContext(ctx, "docker", "exec", container,
			"clickhouse-client", "--query", query)
		b, err := cmd.CombinedOutput()
		if err != nil {
			if dockerExecRetryable(err) {
				return fmt.Errorf("clickhouse exec: %w: %s", err, strings.TrimSpace(string(b)))
			}
			return outbound.Abort(fmt.Errorf("clickhouse exec: %w: %s", err, strings.TrimSpace(string(b))))
		}
		out = b
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("hyperdx: clickhouse fallback via %s: %w", container, err)
	}
	n, err := parseCountOutput(out)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func dockerExecRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "connection") ||
		strings.Contains(msg, "daemon") ||
		strings.Contains(msg, "temporarily")
}

func parseCountOutput(out []byte) (int, error) {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0, nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("hyperdx: parse clickhouse count %q: %w", s, err)
	}
	return n, nil
}

func joinProbeErrors(genPhoenix, plainPhoenix, genHyper, plainHyper int,
	errGenPhoenix, errPlainPhoenix, errGenHyper, errPlainHyper error,
	okGenPhoenix, okPlainAbsentPhoenix, okGenHyper, okPlainHyper bool,
) error {
	var parts []string
	if !okGenPhoenix {
		parts = append(parts, fmt.Sprintf("gen_ai phoenix want >0 got %d err=%v", genPhoenix, errGenPhoenix))
	}
	if !okPlainAbsentPhoenix {
		parts = append(parts, fmt.Sprintf("plain phoenix want 0 got %d err=%v", plainPhoenix, errPlainPhoenix))
	}
	if !okGenHyper {
		parts = append(parts, fmt.Sprintf("gen_ai hyperdx want >0 got %d err=%v", genHyper, errGenHyper))
	}
	if !okPlainHyper {
		parts = append(parts, fmt.Sprintf("plain hyperdx want >0 got %d err=%v", plainHyper, errPlainHyper))
	}
	return errors.New(strings.Join(parts, "; "))
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
