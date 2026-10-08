package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// UIProxy mounts an HTTP UI under a gateway path prefix.
type UIProxy struct {
	Path        string
	URL         string
	Title       string
	Description string
	// StripPrefix removes the public path before forwarding (default true; Phoenix).
	StripPrefix bool
}

type uiProxyFile struct {
	Path        string `yaml:"path"`
	URL         string `yaml:"url"`
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	StripPrefix *bool  `yaml:"strip_prefix"`
}

var reservedUIProxyPrefixes = []string{"/v1", "/health", "/debug", "/media"}

// ParseUIProxies normalizes and validates ui_proxies from config file entries.
func ParseUIProxies(entries []uiProxyFile) ([]UIProxy, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]UIProxy, 0, len(entries))
	seen := make(map[string]struct{})
	for i, e := range entries {
		path := normalizeUIProxyPath(e.Path)
		if path == "" {
			return nil, fmt.Errorf("router: ui_proxies[%d].path required", i)
		}
		if _, ok := seen[path]; ok {
			return nil, fmt.Errorf("router: duplicate ui_proxies path %q", path)
		}
		seen[path] = struct{}{}
		for _, reserved := range reservedUIProxyPrefixes {
			if path == reserved || strings.HasPrefix(path, reserved+"/") {
				return nil, fmt.Errorf("router: ui_proxies path %q conflicts with reserved prefix %q", path, reserved)
			}
		}
		rawURL := ExpandEnv(strings.TrimSpace(e.URL))
		if rawURL == "" {
			return nil, fmt.Errorf("router: ui_proxies[%d].url required", i)
		}
		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("router: ui_proxies[%d].url: %w", i, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("router: ui_proxies[%d].url must be http or https", i)
		}
		if u.Host == "" {
			return nil, fmt.Errorf("router: ui_proxies[%d].url missing host", i)
		}
		title := strings.TrimSpace(e.Title)
		if title == "" {
			title = defaultUIProxyTitle(path)
		}
		desc := strings.TrimSpace(e.Description)
		if desc == "" {
			desc = defaultUIProxyDescription(path)
		}
		stripPrefix := true
		if e.StripPrefix != nil {
			stripPrefix = *e.StripPrefix
		}
		out = append(out, UIProxy{
			Path:        path,
			URL:         strings.TrimRight(u.String(), "/"),
			Title:       title,
			Description: desc,
			StripPrefix: stripPrefix,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return len(out[i].Path) > len(out[j].Path)
	})
	return out, nil
}

func normalizeUIProxyPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		return ""
	}
	return path
}

func defaultUIProxyTitle(path string) string {
	seg := strings.TrimPrefix(path, "/")
	if seg == "" {
		return "UI"
	}
	return strings.ToUpper(seg[:1]) + seg[1:]
}

func defaultUIProxyDescription(path string) string {
	return "Proxied observability UI at " + path + "/"
}
