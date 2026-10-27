package smoke

import (
	"net"
	"net/url"
	"os"
	"strings"
)

// ResolveCollectorOptions fills empty OTLP and observability URLs from operator env.
// When POLYPUS_BASE_URL points at a remote homelab host, observability UIs use plain
// http on tailnet TCP-forwarded ports (6006, 8080) and OTLP HTTP on :4318.
func ResolveCollectorOptions(in CollectorOptions) CollectorOptions {
	out := in
	remoteHost, isRemote := polypusHomelabHost(os.Getenv("POLYPUS_BASE_URL"))

	if strings.TrimSpace(out.OTLPEndpoint) == "" {
		out.OTLPEndpoint = firstNonEmpty(
			os.Getenv("POLYPUS_OTLP_ENDPOINT"),
			os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		)
	}
	if strings.TrimSpace(out.OTLPEndpoint) == "" && isRemote {
		out.OTLPEndpoint = observabilityHTTPBase(remoteHost, "4318")
	}

	if strings.TrimSpace(out.PhoenixBaseURL) == "" {
		out.PhoenixBaseURL = strings.TrimSpace(os.Getenv("PHOENIX_BASE_URL"))
	}
	if strings.TrimSpace(out.PhoenixBaseURL) == "" && isRemote {
		out.PhoenixBaseURL = observabilityHTTPBase(remoteHost, "6006")
	} else if isRemote {
		out.PhoenixBaseURL = coerceObservabilityHTTP(remoteHost, out.PhoenixBaseURL, "6006")
	}

	if strings.TrimSpace(out.HyperDXBaseURL) == "" {
		out.HyperDXBaseURL = strings.TrimSpace(os.Getenv("HYPERDX_BASE_URL"))
	}
	if strings.TrimSpace(out.HyperDXBaseURL) == "" && isRemote {
		out.HyperDXBaseURL = observabilityHTTPBase(remoteHost, "8080")
	} else if isRemote {
		out.HyperDXBaseURL = coerceObservabilityHTTP(remoteHost, out.HyperDXBaseURL, "8080")
	}

	if isRemote && !out.SkipHyperDXVerify {
		out.SkipHyperDXVerify = true
	}
	if isRemote && os.Getenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK") == "" {
		_ = os.Setenv("POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK", "0")
	}

	return out
}

func polypusHomelabHost(baseURL string) (host string, remote bool) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", false
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", false
	}
	h := u.Hostname()
	if h == "" {
		return "", false
	}
	if isLoopbackHost(h) {
		return h, false
	}
	return h, true
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// observabilityHTTPBase is plain HTTP on purpose: tailscale/serve.json forwards the
// collector, Phoenix and HyperDX ports as raw TCP on the tailnet, where WireGuard
// already encrypts the traffic, so those ports have no TLS to speak.
func observabilityHTTPBase(host, port string) string {
	return "http://" + host + ":" + port // NOSONAR go:S5332 tailnet TCP forward, encrypted by WireGuard.
}

func coerceObservabilityHTTP(homelabHost, baseURL, port string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return observabilityHTTPBase(homelabHost, port)
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	if u.Hostname() != homelabHost {
		return baseURL
	}
	return observabilityHTTPBase(homelabHost, port)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
