package router

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

// Default loopback and docker bridge hosts allowed for local backends.
var defaultAllowedHosts = map[string]struct{}{
	"127.0.0.1":            {},
	"localhost":            {},
	"::1":                  {},
	"host.docker.internal": {},
}

// ValidateBackend validates a backend definition against router policy.
func ValidateBackend(b config.BackendDef, policy config.RouterPolicy) error {
	raw := strings.TrimSpace(b.BaseURL)
	if raw == "" {
		return fmt.Errorf("backend url required")
	}
	if b.Remote {
		if policy.RequireCloudOptIn && !config.InferenceCloudCaseAllowed() {
			return fmt.Errorf("remote backend requires INFERENCE_CLOUD_CASE=1")
		}
		return validateRemoteBackendURL(raw)
	}
	return validateLocalBackendURL(raw, policy.RejectNonLoopbackBackends)
}

// ValidateBackendURL rejects cloud or non-loopback hosts for local routing (case-mode default).
func ValidateBackendURL(raw string) error {
	return validateLocalBackendURL(raw, true)
}

func validateRemoteBackendURL(raw string) error {
	u, err := parseBackendURL(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if isBlockedCloudHost(host) {
		return fmt.Errorf("backend host %q is not allowed for remote backends", host)
	}
	return nil
}

func validateLocalBackendURL(raw string, rejectNonLoopback bool) error {
	u, err := parseBackendURL(raw)
	if err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if isBlockedCloudHost(host) {
		return fmt.Errorf("backend host %q is not allowed (cloud speech blocked)", host)
	}
	if !rejectNonLoopback {
		return nil
	}
	if _, ok := defaultAllowedHosts[host]; ok {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("backend host %q not allowed (loopback only for case mode)", host)
}

func parseBackendURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("backend url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("backend url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("backend url scheme must be http or https")
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return nil, fmt.Errorf("backend url host required")
	}
	return u, nil
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
