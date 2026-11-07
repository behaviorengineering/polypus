// Package redact masks credentials in URLs and in text that carries URLs. It is a
// leaf package so that config, observability and the CLI can share one rule set.
package redact

import (
	"net/url"
	"regexp"
	"strings"
)

// Secret replaces a masked credential.
const Secret = "REDACTED"

var embeddedHTTPURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

func isSecretQueryName(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	switch n {
	case "key", "api_key", "apikey", "api-key", "access_token", "token",
		"secret", "password", "auth", "authorization", "client_secret":
		return true
	}
	return strings.Contains(n, "api_key") ||
		strings.HasSuffix(n, "_secret") ||
		strings.HasSuffix(n, "_token") ||
		strings.HasSuffix(n, "_key")
}

func redactQueryValues(q url.Values) bool {
	changed := false
	for name := range q {
		if !isSecretQueryName(name) {
			continue
		}
		q.Set(name, Secret)
		changed = true
	}
	return changed
}

// URL masks userinfo credentials and secret query values in raw. A URL that needs
// neither comes back unchanged. A value that does not parse as a URL comes back
// unchanged too, so callers that print untrusted input should parse it first.
func URL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	changed := false
	if u.User != nil {
		// The user name alone can be a token (https://TOKEN@host), so mask all of it.
		u.User = url.User(Secret)
		changed = true
	}
	if u.RawQuery != "" {
		q := u.Query()
		if redactQueryValues(q) {
			u.RawQuery = q.Encode()
			changed = true
		}
	}
	if !changed {
		return raw
	}
	return u.String()
}

// Query masks secret values in a raw query string such as "key=abc&alt=sse".
func Query(raw string) string {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if !redactQueryValues(q) {
		return raw
	}
	return q.Encode()
}

// Text masks every http or https URL embedded in s with URL.
func Text(s string) string {
	if s == "" || !strings.Contains(s, "://") {
		return s
	}
	return embeddedHTTPURL.ReplaceAllStringFunc(s, URL)
}
