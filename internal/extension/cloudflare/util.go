package cloudflare

import (
	"strings"
)

// NormalizeModel strips Polypus backend_id/ prefix (e.g. cf_local/@cf/...).
func NormalizeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if strings.HasPrefix(model, "@") {
		return model
	}
	if i := strings.Index(model, "/"); i > 0 {
		rest := strings.TrimSpace(model[i+1:])
		if strings.HasPrefix(rest, "@") || strings.Contains(rest, "/") {
			return rest
		}
	}
	return model
}

// AIBaseURL normalizes a Workers AI base URL (accepts .../ai or .../ai/v1).
func AIBaseURL(raw string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	if base == "" {
		return "", errBaseURLRequired
	}
	base = strings.TrimSuffix(base, "/v1")
	if !accountIDRE.MatchString(base) {
		return "", errAccountIDRequired
	}
	return base, nil
}

func runURL(apiBase, model string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "/")
	return strings.TrimRight(apiBase, "/") + "/run/" + model
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
