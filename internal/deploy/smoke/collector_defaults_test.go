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

	out := ResolveCollectorOptions(CollectorOptions{})
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
		t.Fatal("expected SkipHyperDXVerify on remote homelab")
	}
	if os.Getenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK") != "0" {
		t.Fatalf("expected clickhouse fallback disabled, got %q", os.Getenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK"))
	}
}

func TestResolveCollectorOptionsCoercesHTTPSObservabilityURLs(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "https://polypus.example.ts.net/")
	t.Setenv("PHOENIX_BASE_URL", "https://polypus.example.ts.net:6006")
	t.Setenv("HYPERDX_BASE_URL", "https://polypus.example.ts.net:8080")

	out := ResolveCollectorOptions(CollectorOptions{
		PhoenixBaseURL: "https://polypus.example.ts.net:6006",
		HyperDXBaseURL: "https://polypus.example.ts.net:8080",
	})
	if out.PhoenixBaseURL != "http://polypus.example.ts.net:6006" {
		t.Fatalf("PhoenixBaseURL = %q", out.PhoenixBaseURL)
	}
	if out.HyperDXBaseURL != "http://polypus.example.ts.net:8080" {
		t.Fatalf("HyperDXBaseURL = %q", out.HyperDXBaseURL)
	}
}

func TestResolveCollectorOptionsLoopbackUnchanged(t *testing.T) {
	t.Setenv("POLYPUS_BASE_URL", "http://127.0.0.1:1320")
	t.Setenv("POLYPUS_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("PHOENIX_BASE_URL", "")
	t.Setenv("HYPERDX_BASE_URL", "")

	out := ResolveCollectorOptions(CollectorOptions{})
	if out.OTLPEndpoint != "" {
		t.Fatalf("expected empty OTLP for loopback, got %q", out.OTLPEndpoint)
	}
	if out.SkipHyperDXVerify {
		t.Fatal("expected HyperDX verify on loopback")
	}
}
