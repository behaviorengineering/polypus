package otelcol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// SkipHyperDXVerify skips HyperDX routing checks (for example tailnet laptops without /api/v2 or Docker).
	SkipHyperDXVerify bool
	// Logger receives progress (endpoints, trace ids, probe counts). Nil discards it,
	// so the smoke never writes to the host's stdout.
	Logger *slog.Logger
}

// DefaultCollectorPollInterval is the delay between backend polls.
const DefaultCollectorPollInterval = 2 * time.Second

// DefaultCollectorSmokeTimeout is the collector smoke budget.
const DefaultCollectorSmokeTimeout = 90 * time.Second

var (
	errHyperDXAPIUnavailable  = errors.New("hyperdx: v2 search API unavailable")
	errPhoenixRESTUnavailable = errors.New("phoenix: REST spans API unavailable")
)

// RunCollector exports two synthetic traces through otelcol and verifies Phoenix / HyperDX routing.
func RunCollector(ctx context.Context, opts CollectorOptions) error {
	if ctx == nil {
		return fmt.Errorf("collector smoke: context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("collector smoke: missing deadline")
	}
	opts = opts.withCollectorDefaults()

	opts.Logger.Info("collector smoke", "otlp", opts.OTLPEndpoint, "phoenix", opts.PhoenixBaseURL, "hyperdx", opts.HyperDXBaseURL)

	genAITrace, plainTrace, err := exportSmokeTraces(ctx, opts)
	if err != nil {
		return err
	}
	opts.Logger.Info("collector smoke: otlp export ok", "gen_ai_trace_id", genAITrace, "plain_trace_id", plainTrace)

	deadline, _ := ctx.Deadline()
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("collector smoke: %w (last probe: %w)", err, lastErr)
			}
			return fmt.Errorf("collector smoke: %w", err)
		}
		if lastErr = probeRouting(ctx, opts, genAITrace, plainTrace); lastErr == nil {
			opts.Logger.Info("collector smoke: routing ok", "hyperdx_verified", !opts.SkipHyperDXVerify)
			return nil
		}
		if time.Now().Add(opts.PollInterval).After(deadline) {
			return fmt.Errorf("collector smoke: timed out: %w", lastErr)
		}
		if err := sleepContext(ctx, opts.PollInterval); err != nil {
			return err
		}
	}
}

// exportSmokeTraces sends one gen_ai span and one plain span through the collector
// and returns their normalised trace ids.
func exportSmokeTraces(ctx context.Context, opts CollectorOptions) (genAI, plain string, err error) {
	genAI, err = exportCollectorSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-genai",
		attribute.String("gen_ai.system", "polypus-local-smoke"),
		attribute.String("smoke.probe", "gen_ai"),
	)
	if err != nil {
		return "", "", fmt.Errorf("collector smoke: export gen_ai span: %w", err)
	}
	plain, err = exportCollectorSpan(ctx, opts.OTLPEndpoint, "polypus-local-collector-smoke-plain",
		attribute.String("smoke.probe", "plain"),
	)
	if err != nil {
		return "", "", fmt.Errorf("collector smoke: export plain span: %w", err)
	}
	if genAI, err = normalizeTraceIDHex(genAI); err != nil {
		return "", "", fmt.Errorf("collector smoke: gen_ai trace id: %w", err)
	}
	if plain, err = normalizeTraceIDHex(plain); err != nil {
		return "", "", fmt.Errorf("collector smoke: plain trace id: %w", err)
	}
	return genAI, plain, nil
}

// probeResult is one routing check: how many spans a backend holds for a trace, and
// whether that count is what routing should produce.
type probeResult struct {
	name  string // for example "gen_ai phoenix".
	want  string // "0" or ">0".
	count int
	err   error
}

func (p probeResult) ok() bool {
	if p.err != nil {
		return false
	}
	if p.want == "0" {
		return p.count == 0
	}
	return p.count > 0
}

// probeRouting checks the collector's routing once: the gen_ai trace reaches Phoenix
// and the plain one does not, and (unless skipped) both reach HyperDX. It returns nil
// when routing is right, else what is still missing.
func probeRouting(ctx context.Context, opts CollectorOptions, genAITrace, plainTrace string) error {
	results := make([]probeResult, 0, 4)
	add := func(name, want string, count int, err error) {
		results = append(results, probeResult{name: name, want: want, count: count, err: err})
	}
	n, err := phoenixSpanCount(ctx, opts, genAITrace)
	add("gen_ai phoenix", ">0", n, err)
	n, err = phoenixSpanCount(ctx, opts, plainTrace)
	add("plain phoenix", "0", n, err)
	if !opts.SkipHyperDXVerify {
		n, err = hyperdxTraceCount(ctx, opts, genAITrace)
		add("gen_ai hyperdx", ">0", n, err)
		n, err = hyperdxTraceCount(ctx, opts, plainTrace)
		add("plain hyperdx", ">0", n, err)
	}
	attrs := make([]any, 0, 2*len(results))
	for _, r := range results {
		attrs = append(attrs, strings.ReplaceAll(r.name, " ", "_"), r.count)
	}
	opts.Logger.Info("collector smoke: probe counts", attrs...)
	return joinProbeErrors(results)
}

// joinProbeErrors describes every failed probe, or returns nil when all passed.
func joinProbeErrors(results []probeResult) error {
	var parts []string
	for _, r := range results {
		if !r.ok() {
			parts = append(parts, fmt.Sprintf("%s want %s got %d err=%v", r.name, r.want, r.count, r.err))
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
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
	if out.Logger == nil {
		out.Logger = slog.New(slog.DiscardHandler)
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
		_ = tp.Shutdown(ctx) // the caller's deadline bounds the final export.
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
	n, err := phoenixSpanCountREST(ctx, opts, traceID)
	if err == nil {
		return n, nil
	}
	if errors.Is(err, errPhoenixRESTUnavailable) {
		return phoenixSpanCountGraphQL(ctx, opts, traceID)
	}
	return 0, err
}

func phoenixSpanCountREST(ctx context.Context, opts CollectorOptions, traceID string) (int, error) {
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
	req.Header.Set("Accept", "application/json")
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
		return 0, errPhoenixRESTUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("phoenix: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	trim := strings.TrimSpace(string(body))
	if strings.HasPrefix(trim, "<") || strings.HasPrefix(strings.ToLower(trim), "<!doctype") {
		return 0, errPhoenixRESTUnavailable
	}

	var parsed struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, errPhoenixRESTUnavailable
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
		cmd := exec.CommandContext(ctx, "docker", "exec", container, //nolint:gosec // G204: traceID is normalised hex and container is the smoke's own name
			"clickhouse-client", "--query", query)
		b, err := cmd.CombinedOutput()
		if err != nil {
			if dockerExecRetryable(err) {
				return outbound.Retry(fmt.Errorf("clickhouse exec: %w: %s", err, strings.TrimSpace(string(b))))
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

// dockerExitDaemon is the exit status docker gives when docker itself failed (daemon
// unreachable, container not running yet) rather than the command it ran.
const dockerExitDaemon = 125

// dockerExecRetryable reports a docker exec failure worth retrying: docker itself
// failed, which a starting stack outgrows. A failed query (any other exit status) or
// a missing docker binary is not retried.
func dockerExecRetryable(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == dockerExitDaemon
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
