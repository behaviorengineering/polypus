// Package httpreq reads the public host and scheme a gateway request arrived on,
// including the first X-Forwarded-* value behind Tailscale Serve.
package httpreq

import (
	"net"
	"net/http"
	"strings"
)

// URL schemes a request can arrive on.
const (
	HTTP  = "http"
	HTTPS = "https"
)

// FirstForwarded is the first value of a comma-separated X-Forwarded-* header.
func FirstForwarded(v string) string {
	first, _, _ := strings.Cut(v, ",")
	return strings.TrimSpace(first)
}

// Scheme is the scheme the client used: the proxy's X-Forwarded-Proto, else TLS,
// else http. The landing page, the UI proxies and the admin same-origin check
// all read it here.
func Scheme(r *http.Request) string {
	if r == nil {
		return HTTP
	}
	if proto := strings.ToLower(FirstForwarded(r.Header.Get("X-Forwarded-Proto"))); proto == HTTPS || proto == HTTP {
		return proto
	}
	if r.TLS != nil {
		return HTTPS
	}
	return HTTP
}

// Hostname is the public host the request reached, without a port. Unsafe or
// empty values fall back to 127.0.0.1 so landing links never echo a header.
func Hostname(r *http.Request) string {
	const fallback = "127.0.0.1"
	if r == nil {
		return fallback
	}
	raw := strings.TrimSpace(r.Host)
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Host)
	}
	if raw == "" {
		return fallback
	}
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		host = raw
	}
	host = strings.Trim(host, "[]")
	if !safeHostname(host) {
		return fallback
	}
	return host
}

func safeHostname(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.' || r == '-':
		default:
			return false
		}
	}
	return true
}
