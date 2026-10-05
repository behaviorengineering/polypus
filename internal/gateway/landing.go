package gateway

import (
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/media"
)

const bannerAssetPath = "/media/banner.webp"

type landingLink struct {
	Href        string
	Title       string
	Description string
}

type landingPageData struct {
	Links []landingLink
}

const landingPageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Polypus</title>
<style>
html, body { margin: 0; min-height: 100%; background: #000; color: #e8e8e8; font-family: system-ui, sans-serif; }
main { max-width: 42rem; margin: 0 auto; padding: 2rem 1.25rem 3rem; text-align: center; }
img.banner { display: block; max-width: min(100%, 28rem); height: auto; margin: 0 auto 2rem; }
nav { text-align: left; }
ul { list-style: none; padding: 0; margin: 0; }
li { margin: 0 0 1.25rem; }
a { color: #7eb8ff; text-decoration: none; font-weight: 600; }
a:hover { text-decoration: underline; }
p.desc { margin: 0.35rem 0 0; font-size: 0.9rem; color: #a8a8a8; line-height: 1.45; }
</style>
</head>
<body>
<main>
<img class="banner" src="` + bannerAssetPath + `" width="1536" height="753" alt="Polypus">
<nav>
<ul>
{{range .Links}}
<li><a href="{{.Href}}">{{.Title}}</a><p class="desc">{{.Description}}</p></li>
{{end}}
</ul>
</nav>
</main>
</body>
</html>`

var landingPageTemplate = template.Must(template.New("landing").Parse(landingPageTmpl))

func landingLinksForRequest(r *http.Request) []landingLink {
	base := siblingUIBaseURL(r)
	return []landingLink{
		{
			Href:        "/health",
			Title:       "Health",
			Description: "Gateway liveness JSON (no upstream dials).",
		},
		{
			Href:        "/health/backends",
			Title:       "Backend health",
			Description: "Probes each configured backend (and Switchyard when enabled).",
		},
		{
			Href:        "/health/upstreams",
			Title:       "Upstream breakers",
			Description: "Circuit-breaker state without dialing leaf backends.",
		},
		{
			Href:        "/v1/models",
			Title:       "Models",
			Description: "OpenAI-compatible catalog of models enabled on this gateway.",
		},
		{
			Href:        base + ":6006/",
			Title:       "Phoenix (Arize)",
			Description: "OpenInference LLM traces for chat and router spans (OTLP gRPC :4317).",
		},
		{
			Href:        base + ":8080/",
			Title:       "HyperDX (OpenTelemetry)",
			Description: "App traces and logs (OTLP gRPC :4319, HTTP :4318).",
		},
	}
}

func siblingUIBaseURL(r *http.Request) string {
	scheme := requestScheme(r)
	host := requestHostname(r)
	return scheme + "://" + host
}

func requestScheme(r *http.Request) string {
	if r == nil {
		return "http"
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		if i := strings.Index(proto, ","); i >= 0 {
			proto = strings.TrimSpace(proto[:i])
		}
		if proto == "https" || proto == "http" {
			return proto
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func requestHostname(r *http.Request) string {
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

func (g *Gateway) serveLanding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := landingPageData{Links: landingLinksForRequest(r)}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	if err := landingPageTemplate.Execute(w, data); err != nil {
		return
	}
}

func (g *Gateway) serveBannerWebP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "image/webp")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(media.BannerWebP)))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(media.BannerWebP)
}
