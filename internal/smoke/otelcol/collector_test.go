package otelcol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/outbound"
)

func TestPhoenixSpanCountREST(t *testing.T) {
	const traceID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1/projects/default/spans") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("trace_id") != traceID {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]string{{"name": "smoke"}},
		})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := phoenixSpanCountREST(ctx, CollectorOptions{
		PhoenixBaseURL: srv.URL,
		PhoenixProject: "default",
	}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 span, got %d", n)
	}
}

func TestPhoenixSpanCountGraphQLFallback(t *testing.T) {
	const traceID = "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/v1/projects/default/spans"):
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<!DOCTYPE html><html></html>"))
		case r.URL.Path == "/graphql" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"projects": map[string]interface{}{
						"edges": []map[string]interface{}{
							{"node": map[string]interface{}{
								"name": "default",
								"trace": map[string]interface{}{
									"spans": map[string]interface{}{
										"edges": []map[string]interface{}{
											{"node": map[string]string{"id": "span-1"}},
										},
									},
								},
							}},
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := phoenixSpanCount(ctx, CollectorOptions{
		PhoenixBaseURL: srv.URL,
		PhoenixProject: "default",
	}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 span, got %d", n)
	}
}

func TestHyperdxSearchV2(t *testing.T) {
	const traceID = "fedcba9876543210fedcba9876543210"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/sources":
			_ = json.NewEncoder(w).Encode([]hyperdxSource{{
				ID: "trace-src", Name: "Traces", Kind: "trace",
			}})
		case "/api/v2/search":
			_ = json.NewEncoder(w).Encode([]map[string]string{
				{"TraceId": traceID},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := hyperdxSearchV2(ctx, CollectorOptions{HyperDXBaseURL: srv.URL}, traceID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
}

func TestNormalizeTraceIDHex(t *testing.T) {
	id, err := normalizeTraceIDHex("0123456789ABCDEF0123456789abcdef")
	if err != nil || id != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("normalize: %v id=%q", err, id)
	}
	if _, err := normalizeTraceIDHex("short"); err == nil {
		t.Fatal("expected error for short id")
	}
	if _, err := normalizeTraceIDHex("0123456789abcdef0123456789abcdeg"); err == nil {
		t.Fatal("expected error for non-hex")
	}
}

func TestPickHyperDXTraceSource(t *testing.T) {
	id := pickHyperDXTraceSource([]hyperdxSource{
		{ID: "log", Name: "Logs", Kind: "log"},
		{ID: "tr", Name: "OTel Traces", Kind: "trace"},
	})
	if id != "tr" {
		t.Fatalf("expected trace source tr, got %q", id)
	}
}

func TestJoinProbeErrorsNamesOnlyTheFailedProbes(t *testing.T) {
	results := []probeResult{
		{name: "gen_ai phoenix", want: ">0", count: 1},
		{name: "plain phoenix", want: "0", count: 2},
		{name: "gen_ai hyperdx", want: ">0", err: errors.New("down")},
	}
	err := joinProbeErrors(results)
	if err == nil || strings.Contains(err.Error(), "gen_ai phoenix") ||
		!strings.Contains(err.Error(), "plain phoenix want 0 got 2") || !strings.Contains(err.Error(), "gen_ai hyperdx") {
		t.Fatalf("got %v", err)
	}
	if err := joinProbeErrors(results[:1]); err != nil {
		t.Fatalf("all probes passed: %v", err)
	}
}

func TestDockerExecRetryableReadsTheExitStatus(t *testing.T) {
	daemonFailed := exec.Command("sh", "-c", "exit 125").Run()
	queryFailed := exec.Command("sh", "-c", "exit 1").Run()
	if !dockerExecRetryable(fmt.Errorf("wrapped: %w", daemonFailed)) {
		t.Fatal("exit 125 means docker itself failed: retry")
	}
	if dockerExecRetryable(queryFailed) {
		t.Fatal("a failed query is not retried")
	}
	if dockerExecRetryable(errors.New("connection timeout, says some text")) {
		t.Fatal("text alone is not a docker failure")
	}
}

// endpoints are the URLs a smoke needs, so withCollectorDefaults has nothing to fill.
var endpoints = CollectorOptions{OTLPEndpoint: "http://otlp.test:4317", PhoenixBaseURL: "http://phoenix.test", HyperDXBaseURL: "http://hyperdx.test"}

func TestCollectorDefaultsKeepTheCallersFallbackChoice(t *testing.T) {
	t.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", "")
	withDisable := endpoints
	withDisable.DisableHyperDXCH = true
	if got, err := withDisable.withCollectorDefaults(); err != nil || !got.DisableHyperDXCH {
		t.Fatalf("a caller's DisableHyperDXCH must survive the defaults (err %v)", err)
	}
	if got, err := endpoints.withCollectorDefaults(); err != nil || got.DisableHyperDXCH {
		t.Fatalf("the zero value allows the fallback (err %v)", err)
	}
	for _, off := range []string{"0", "off", "No"} {
		t.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", off)
		if got, err := endpoints.withCollectorDefaults(); err != nil || !got.DisableHyperDXCH {
			t.Fatalf("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK=%s must turn the fallback off (err %v)", off, err)
		}
	}
	t.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", "maybe")
	if _, err := endpoints.withCollectorDefaults(); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("an unknown word must be an error, got %v", err)
	}
}

func TestCollectorDefaultsHaveNoBuiltInEndpoints(t *testing.T) {
	for _, k := range []string{"POLYPUS_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT", "PHOENIX_BASE_URL", "HYPERDX_BASE_URL", "POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK"} {
		t.Setenv(k, "")
	}
	_, err := CollectorOptions{}.withCollectorDefaults()
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("want CodeInvalid naming the unset endpoints, got %v", err)
	}
	for _, name := range []string{"POLYPUS_OTLP_ENDPOINT", "PHOENIX_BASE_URL", "HYPERDX_BASE_URL"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("err %v must name %s", err, name)
		}
	}
	if _, err := (CollectorOptions{OTLPEndpoint: "x", PhoenixBaseURL: "y", SkipHyperDXVerify: true}).withCollectorDefaults(); err != nil {
		t.Fatalf("HyperDX is not needed when its verify is skipped: %v", err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://std.test:4317")
	t.Setenv("PHOENIX_BASE_URL", "http://phoenix.env")
	t.Setenv("HYPERDX_BASE_URL", "http://hyperdx.env")
	got, err := CollectorOptions{}.withCollectorDefaults()
	if err != nil || got.OTLPEndpoint != "http://std.test:4317" || got.PhoenixBaseURL != "http://phoenix.env" || got.HyperDXBaseURL != "http://hyperdx.env" {
		t.Fatalf("env endpoints: %+v err %v", got, err)
	}
}

func TestRunCollectorErrorsAreTyped(t *testing.T) {
	//nolint:staticcheck // SA1012: the nil context is the case under test.
	if err := RunCollector(nil, endpoints); derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("nil ctx: %v", err)
	}
	if err := RunCollector(context.Background(), endpoints); derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("no deadline: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bad := endpoints
	bad.OTLPEndpoint = "grpc://"
	if err := RunCollector(ctx, bad); derrors.CodeOf(err) != derrors.CodeUnavailable {
		t.Fatalf("export failure: %v", err)
	}
}

func TestFetchRejectsAnOversizedAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", probeBodyMax+1)))
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (CollectorOptions{}).fetch(ctx, req); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("err = %v, want an oversized answer refused", err)
	}
}

func TestHyperDXTraceCountSkipsTheDisabledFallback(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	opts := CollectorOptions{HyperDXBaseURL: srv.URL, DisableHyperDXCH: true, HyperDXContainer: "no-such-container"}
	_, err := hyperdxTraceCount(ctx, opts, "0123456789abcdef0123456789abcdef")
	if !errors.Is(err, errHyperDXAPIUnavailable) {
		t.Fatalf("with the fallback off the API error is returned as is, got %v", err)
	}
}

func TestDecodeListOrData(t *testing.T) {
	for _, body := range []string{`[1,2]`, `{"data":[1,2]}`} {
		got, err := decodeListOrData[int]([]byte(body))
		if err != nil || len(got) != 2 {
			t.Fatalf("%s: %v %v", body, got, err)
		}
	}
	if _, err := decodeListOrData[int]([]byte(`"nope"`)); err == nil {
		t.Fatal("want an error for neither shape")
	}
}

// refusingTransport refuses the first two connections, as a backend still starting
// does, then sends on.
type refusingTransport struct {
	calls atomic.Int32
	next  http.RoundTripper
}

func (c *refusingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if c.calls.Add(1) < 3 {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	}
	return c.next.RoundTrip(r)
}

func TestFetchUsesTheInjectedClientAndPolicies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	transport := &refusingTransport{next: srv.Client().Transport}
	opts := CollectorOptions{
		HTTP:     &http.Client{Transport: transport},
		Policies: outbound.NewPoliciesWith(outbound.Backoff{Min: time.Millisecond, Max: time.Millisecond}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	ans, err := opts.fetch(ctx, req)
	if err != nil || !ans.ok() || string(ans.body) != "ok" {
		t.Fatalf("fetch: %+v, %v", ans, err)
	}
	if transport.calls.Load() != 3 {
		t.Fatalf("the injected client sent %d requests, want 3", transport.calls.Load())
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the injected policies should retry on their own backoff, took %s", time.Since(start))
	}
}
