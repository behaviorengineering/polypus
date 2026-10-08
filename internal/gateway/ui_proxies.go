package gateway

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
)

type uiProxyMount struct {
	prefix  string
	handler http.Handler
	meta    config.UIProxy
}

func buildUIMounts(proxies []config.UIProxy) ([]uiProxyMount, error) {
	if len(proxies) == 0 {
		return nil, nil
	}
	mounts := make([]uiProxyMount, 0, len(proxies))
	for _, p := range proxies {
		target, err := url.Parse(p.URL)
		if err != nil {
			return nil, fmt.Errorf("gateway: ui_proxies %q: %w", p.Path, err)
		}
		mounts = append(mounts, uiProxyMount{
			prefix:  p.Path,
			handler: newUIReverseProxy(p.Path, target),
			meta:    p,
		})
	}
	return mounts, nil
}

func newUIReverseProxy(prefix string, target *url.URL) http.Handler {
	prefix = strings.TrimSuffix(prefix, "/")
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			inPath := pr.In.URL.Path
			suffix := strings.TrimPrefix(inPath, prefix)
			if suffix == "" {
				suffix = "/"
			} else if !strings.HasPrefix(suffix, "/") {
				suffix = "/" + suffix
			}
			pr.Out.URL.Path = suffix
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			if prefix != "" {
				pr.Out.Header.Set("X-Forwarded-Prefix", prefix)
			}
			if proto := requestScheme(pr.In); proto != "" {
				pr.Out.Header.Set("X-Forwarded-Proto", proto)
			}
			if host := strings.TrimSpace(pr.In.Host); host != "" {
				pr.Out.Header.Set("X-Forwarded-Host", host)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, fmt.Sprintf("polypus ui proxy unavailable: %v", err), http.StatusBadGateway)
		},
	}
}

func (g *Gateway) matchUIProxy(r *http.Request) *uiProxyMount {
	if g == nil || g.shared == nil || len(g.uiMounts) == 0 || r == nil {
		return nil
	}
	path := r.URL.Path
	var best *uiProxyMount
	for i := range g.uiMounts {
		m := &g.uiMounts[i]
		p := m.prefix
		if path == p || strings.HasPrefix(path, p+"/") {
			if best == nil || len(p) > len(best.prefix) {
				best = m
			}
		}
	}
	return best
}

func (g *Gateway) serveUIProxy(w http.ResponseWriter, r *http.Request) bool {
	m := g.matchUIProxy(r)
	if m == nil {
		return false
	}
	m.handler.ServeHTTP(w, r)
	return true
}
