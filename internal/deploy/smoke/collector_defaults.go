package smoke

import (
	"net"
	"net/url"
	"os"
	"strings"
)

// clickHouseFallbackEnv is the operator's switch for the collector smoke's HyperDX
// ClickHouse fallback; when it is set, otelcol reads it and this package leaves the
// choice alone.
const clickHouseFallbackEnv = "POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK"

// ResolveCollectorOptions fills empty OTLP and observability URLs from operator env.
// gatewayURL is the deployed gateway (empty reads POLYPUS_BASE_URL). When it is a
// remote host, such as the environment's tailnet address, observability UIs use plain
// http on tailnet TCP-forwarded ports (6006, 8080) and OTLP HTTP on :4318. When it is
// a loopback host, the local stack (go tool task serve, docker-compose.yml) is
// assumed on that host: OTLP gRPC on OTELCOL_OTLP_GRPC_PORT (4317), Phoenix on
// PHOENIX_PORT (6006) and HyperDX on HYPERDX_PORT (8080). With no gateway named,
// nothing is assumed and otelcol names what is unset.
//
// For a remote gateway it also turns the ClickHouse fallback off (DisableHyperDXCH),
// unless the operator set POLYPUS_HYPERDX_CLICKHOUSE_FALLBACK: the fallback runs
// docker exec on this machine, where a remote gateway's HyperDX does not run. The
// process environment is never changed.
func ResolveCollectorOptions(in CollectorOptions, gatewayURL string) CollectorOptions {
	out := in
	if strings.TrimSpace(gatewayURL) == "" {
		gatewayURL = os.Getenv("POLYPUS_BASE_URL")
	}
	remoteHost, isRemote := remoteGatewayHost(gatewayURL)

	if strings.TrimSpace(out.OTLPEndpoint) == "" {
		out.OTLPEndpoint = firstNonEmpty(
			os.Getenv("POLYPUS_OTLP_ENDPOINT"),
			os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		)
	}
	isLoopback := remoteHost != "" && !isRemote
	if strings.TrimSpace(out.OTLPEndpoint) == "" && isRemote {
		out.OTLPEndpoint = observabilityHTTPBase(remoteHost, "4318")
	}
	if strings.TrimSpace(out.OTLPEndpoint) == "" && isLoopback {
		out.OTLPEndpoint = observabilityHTTPBase(remoteHost, localPort("OTELCOL_OTLP_GRPC_PORT", "4317"))
	}

	if strings.TrimSpace(out.PhoenixBaseURL) == "" {
		out.PhoenixBaseURL = strings.TrimSpace(os.Getenv("PHOENIX_BASE_URL"))
	}
	if strings.TrimSpace(out.PhoenixBaseURL) == "" && isRemote {
		out.PhoenixBaseURL = observabilityHTTPBase(remoteHost, "6006")
	} else if isRemote {
		out.PhoenixBaseURL = coerceObservabilityHTTP(remoteHost, out.PhoenixBaseURL, "6006")
	}
	if strings.TrimSpace(out.PhoenixBaseURL) == "" && isLoopback {
		out.PhoenixBaseURL = observabilityHTTPBase(remoteHost, localPort("PHOENIX_PORT", "6006"))
	}

	if strings.TrimSpace(out.HyperDXBaseURL) == "" {
		out.HyperDXBaseURL = strings.TrimSpace(os.Getenv("HYPERDX_BASE_URL"))
	}
	if strings.TrimSpace(out.HyperDXBaseURL) == "" && isRemote {
		out.HyperDXBaseURL = observabilityHTTPBase(remoteHost, "8080")
	} else if isRemote {
		out.HyperDXBaseURL = coerceObservabilityHTTP(remoteHost, out.HyperDXBaseURL, "8080")
	}
	if strings.TrimSpace(out.HyperDXBaseURL) == "" && isLoopback {
		out.HyperDXBaseURL = observabilityHTTPBase(remoteHost, localPort("HYPERDX_PORT", "8080"))
	}

	if isRemote {
		out.SkipHyperDXVerify = true
	}
	if isRemote && os.Getenv(clickHouseFallbackEnv) == "" {
		out.DisableHyperDXCH = true
	}
	return out
}

// localPort reads a local stack port the way docker-compose.yml does: env, else def.
func localPort(env, def string) string {
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v
	}
	return def
}

// remoteGatewayHost returns the gateway's host and whether it is another machine
// (such as the environment's polypus-<identifier> tailnet address). A loopback host
// comes back with remote false; no host comes back empty.
func remoteGatewayHost(baseURL string) (host string, remote bool) {
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

// observabilityHTTPBase is plain HTTP on purpose: the Tailscale serve config (tsserve) forwards the
// collector, Phoenix and HyperDX ports as raw TCP on the tailnet, where WireGuard
// already encrypts the traffic, so those ports have no TLS to speak. The local stack
// on a loopback host has no TLS either.
func observabilityHTTPBase(host, port string) string {
	return "http://" + net.JoinHostPort(host, port) // NOSONAR go:S5332 tailnet TCP forward (WireGuard) or loopback.
}

func coerceObservabilityHTTP(gatewayHost, baseURL, port string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return observabilityHTTPBase(gatewayHost, port)
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL
	}
	if u.Hostname() != gatewayHost {
		return baseURL
	}
	return observabilityHTTPBase(gatewayHost, port)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
