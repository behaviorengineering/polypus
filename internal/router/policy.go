package router

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Default loopback and docker bridge hosts allowed for speech backends.
var defaultAllowedHosts = map[string]struct{}{
	"127.0.0.1":              {},
	"localhost":              {},
	"::1":                    {},
	"host.docker.internal":   {},
}

// ValidateBackendURL rejects cloud or non-loopback hosts for case-mode local routing.
func ValidateBackendURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("backend url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("backend url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("backend url scheme must be http or https")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("backend url host required")
	}
	if isBlockedCloudHost(host) {
		return fmt.Errorf("backend host %q is not allowed (cloud speech blocked)", host)
	}
	if _, ok := defaultAllowedHosts[host]; ok {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("backend host %q not allowed (loopback only for case mode)", host)
}

func isBlockedCloudHost(host string) bool {
	blocked := []string{
		"api.openai.com",
		"openai.com",
		"api.anthropic.com",
		"anthropic.com",
		"generativelanguage.googleapis.com",
	}
	for _, b := range blocked {
		if host == b || strings.HasSuffix(host, "."+b) {
			return true
		}
	}
	return false
}

// OpenAIBaseURL normalizes the backend root (no /v1 suffix; Bifrost adds /v1/... paths).
func OpenAIBaseURL(backend string) string {
	return strings.TrimRight(strings.TrimSpace(backend), "/")
}
