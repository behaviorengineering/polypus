package smoke

import (
	"os"
	"testing"
)

func TestResolveCollectorOptionsRemotePolypusBase(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "https://polypus.example.ts.net/")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PHOENIX_BASE_URL", "")
	t.Setenv("HYPERDX_BASE_URL", "")
	t.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", "")

	out := ResolveCollectorOptions(CollectorOptions{}, "")
	if out.OTLPEndpoint != "http://polypus.example.ts.net:4318" {
		t.Fatalf("OTLPEndpoint = %q", out.OTLPEndpoint)
	}
	if out.PhoenixBaseURL != "http://polypus.example.ts.net:6006" {
		t.Fatalf("PhoenixBaseURL = %q", out.PhoenixBaseURL)
	}
	if out.HyperDXBaseURL != "http://polypus.example.ts.net:8080" {
		t.Fatalf("HyperDXBaseURL = %q", out.HyperDXBaseURL)
	}
	if !out.SkipHyperDXVerify {
		t.Fatal("expected SkipHyperDXVerify on remote gateway")
	}
	if !out.DisableHyperDXCH {
		t.Fatal("expected clickhouse fallback disabled on remote gateway")
	}
	if got := os.Getenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK"); got != "" {
		t.Fatalf("process environment changed: POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK=%q", got)
	}
}

func TestResolveCollectorOptionsRespectsOperatorFallbackSetting(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "https://polypus.example.ts.net/")
	t.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", "1")

	out := ResolveCollectorOptions(CollectorOptions{}, "")
	if out.DisableHyperDXCH {
		t.Fatal("operator set the fallback env; it must not be overridden")
	}
}

func TestResolveCollectorOptionsCoercesHTTPSObservabilityURLs(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "https://polypus.example.ts.net/")
	t.Setenv("PHOENIX_BASE_URL", "https://polypus.example.ts.net:6006")
	t.Setenv("HYPERDX_BASE_URL", "https://polypus.example.ts.net:8080")

	t.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", "")
	out := ResolveCollectorOptions(CollectorOptions{
		PhoenixBaseURL: "https://polypus.example.ts.net:6006",
		HyperDXBaseURL: "https://polypus.example.ts.net:8080",
	}, "")
	if out.PhoenixBaseURL != "http://polypus.example.ts.net:6006" {
		t.Fatalf("PhoenixBaseURL = %q", out.PhoenixBaseURL)
	}
	if out.HyperDXBaseURL != "http://polypus.example.ts.net:8080" {
		t.Fatalf("HyperDXBaseURL = %q", out.HyperDXBaseURL)
	}
}

func TestResolveCollectorOptionsLoopbackUsesTheLocalStack(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "http://127.0.0.1:1320")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PHOENIX_BASE_URL", "")
	t.Setenv("HYPERDX_BASE_URL", "")
	t.Setenv("OTELCOL_OTLP_GRPC_PORT", "")
	t.Setenv("PHOENIX_PORT", "")
	t.Setenv("HYPERDX_PORT", "")

	out := ResolveCollectorOptions(CollectorOptions{}, "")
	if out.OTLPEndpoint != "http://127.0.0.1:4317" {
		t.Fatalf("OTLPEndpoint = %q, want the local otelcol gRPC port", out.OTLPEndpoint)
	}
	if out.PhoenixBaseURL != "http://127.0.0.1:6006" || out.HyperDXBaseURL != "http://127.0.0.1:8080" {
		t.Fatalf("Phoenix %q, HyperDX %q; want the local stack", out.PhoenixBaseURL, out.HyperDXBaseURL)
	}
	if out.SkipHyperDXVerify || out.DisableHyperDXCH {
		t.Fatal("expected HyperDX verify and its ClickHouse fallback on loopback")
	}
}

func TestResolveCollectorOptionsLoopbackFollowsTheStackPorts(t *testing.T) {
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PHOENIX_BASE_URL", "")
	t.Setenv("HYPERDX_BASE_URL", "http://127.0.0.1:9999")
	t.Setenv("OTELCOL_OTLP_GRPC_PORT", "14317")
	t.Setenv("PHOENIX_PORT", "16006")
	t.Setenv("HYPERDX_PORT", "18080")

	out := ResolveCollectorOptions(CollectorOptions{}, "http://localhost:1320")
	if out.OTLPEndpoint != "http://localhost:14317" || out.PhoenixBaseURL != "http://localhost:16006" {
		t.Fatalf("OTLP %q, Phoenix %q; want the overridden ports", out.OTLPEndpoint, out.PhoenixBaseURL)
	}
	if out.HyperDXBaseURL != "http://127.0.0.1:9999" {
		t.Fatalf("HyperDXBaseURL = %q, want HYPERDX_BASE_URL kept", out.HyperDXBaseURL)
	}
}

func TestResolveCollectorOptionsNoGatewayAssumesNothing(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PHOENIX_BASE_URL", "")
	t.Setenv("HYPERDX_BASE_URL", "")

	out := ResolveCollectorOptions(CollectorOptions{}, "")
	if out.OTLPEndpoint != "" || out.PhoenixBaseURL != "" || out.HyperDXBaseURL != "" {
		t.Fatalf("got %+v, want every endpoint left for otelcol to report", out)
	}
}
