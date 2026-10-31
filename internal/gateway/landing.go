package gateway

import (
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/buildinfo"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/media"
)

const bannerAssetPath = "/media/banner.webp"

// The Polypus site, linked from the page header.
const landingDocsURL = "https://behaviorengineering.github.io/polypus/"

type landingLink struct {
	Href        string
	Title       string
	Description string
}

type landingPageData struct {
	ModelLinks []landingLink
	OpsLinks   []landingLink
	OTLPNote   string
	Version    string
}

// The Polypus mark: the eye from media/banner.webp, as drawn for the site (site/assets/polypus-eye.svg).
const landingEyeSVG = `<svg viewBox="-54 -54 108 108" fill="none" stroke="currentColor" stroke-linecap="round" aria-hidden="true">` +
	`<ellipse cx="0" cy="0" rx="41" ry="44.5" stroke-width="13"/><circle cx="0" cy="4" r="10" fill="currentColor" stroke="none"/>` +
	`<path d="M7.3 -22.3A23.5 23.5 0 0 1 20.8 -11" stroke-width="7"/></svg>`

// The same mark in the brand purple, URL-encoded for a data: favicon.
const landingEyeFavicon = `%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='-54 -54 108 108' fill='none' stroke='%23bd50d1' stroke-linecap='round'%3E` +
	`%3Cellipse rx='41' ry='44.5' stroke-width='13'/%3E%3Ccircle cy='4' r='10' fill='%23bd50d1' stroke='none'/%3E` +
	`%3Cpath d='M7.3 -22.3A23.5 23.5 0 0 1 20.8 -11' stroke-width='7'/%3E%3C/svg%3E`

const landingPageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Polypus</title>
<link rel="icon" href="data:image/svg+xml,` + landingEyeFavicon + `">
<style>
:root {
  --brand: #bd50d1; --brand-strong: #9b2fb2; --bg: #120c15; --card: #1a1220; --card-hi: #221829;
  --line: #2c2232; --fg: #ece4f0; --muted: #a99bb0; --link: #f3e8f6; --link-hover: #e8c4ff; --code: #0e0911;
  --heading: "Montserrat", system-ui, sans-serif;
}
* { box-sizing: border-box; }
html, body { margin: 0; min-height: 100%; }
body {
  background: radial-gradient(900px 380px at 50% -80px, rgba(189, 80, 209, 0.22), transparent 70%), var(--bg);
  color: var(--fg); font: 15px/1.55 system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; -webkit-font-smoothing: antialiased;
}
a { color: var(--link); text-decoration: none; }
header.top { border-bottom: 1px solid var(--line); background: rgba(29, 19, 33, 0.85); }
header.top .inner { display: flex; align-items: center; gap: 12px; max-width: 64rem; margin: 0 auto; padding: 0.8rem 1.25rem; }
.wordmark { display: flex; align-items: center; gap: 8px; color: #fff; font: 500 1.25rem/1 var(--heading); letter-spacing: -0.02em; }
.wordmark svg { width: 24px; height: 24px; color: var(--brand); }
.pill { padding: 2px 9px; border: 1px solid var(--line); border-radius: 999px; color: var(--muted); font-size: 0.78rem; }
header.top .docs { margin-left: auto; color: var(--muted); font-size: 0.9rem; }
header.top .docs:hover { color: #fff; }
main { max-width: 64rem; margin: 0 auto; padding: 1.75rem 1.25rem 3rem; }
.layout { display: grid; grid-template-columns: 1fr 1fr; gap: 1.25rem; align-items: start; }
@media (max-width: 760px) { .layout { grid-template-columns: 1fr; } }
.col { display: grid; gap: 1.25rem; min-width: 0; }
.card, section.notes, section.allow { padding: 1.25rem; border: 1px solid var(--line); border-radius: 12px; background: var(--card); }
.hero { padding: 0.5rem; background: linear-gradient(160deg, #2f0035, #1d1321 70%); overflow: hidden; }
img.banner { display: block; width: 100%; height: auto; mix-blend-mode: screen; filter: brightness(0.92) contrast(1.35);
  -webkit-mask-image: radial-gradient(ellipse 62% 64% at 50% 50%, #000 62%, transparent 100%);
  mask-image: radial-gradient(ellipse 62% 64% at 50% 50%, #000 62%, transparent 100%); }
h2 { margin: 0 0 0.6rem; color: var(--brand); font: 600 0.74rem/1.4 var(--heading); letter-spacing: 0.12em; text-transform: uppercase; }
ul { list-style: none; padding: 0; margin: 0 -0.6rem; }
li a { display: block; padding: 0.6rem; border-radius: 8px; }
li a:hover { background: var(--card-hi); }
li a .title { color: var(--link); font-weight: 600; }
li a:hover .title { color: var(--link-hover); }
li a .title::after { content: " \2192"; color: var(--brand); opacity: 0; transition: opacity 0.15s; }
li a:hover .title::after { opacity: 1; }
p.desc { margin: 0.2rem 0 0; color: var(--muted); font-size: 0.88rem; }
pre.note { margin: 0; padding: 0.8rem; border: 1px solid var(--line); border-radius: 8px; background: var(--code); color: #d9cbe0;
  font: 0.8rem/1.6 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; overflow-x: auto; white-space: pre; }
section.allow h2 { margin-bottom: 0.9rem; }
#allow-form label { display: block; margin-bottom: 0.8rem; color: var(--muted); font-size: 0.85rem; }
#allow-form input { display: block; width: 100%; margin-top: 0.3rem; padding: 0.55rem 0.7rem; border: 1px solid var(--line); border-radius: 8px;
  background: var(--code); color: var(--fg); font: inherit; }
#allow-form input:focus { outline: none; border-color: var(--brand); box-shadow: 0 0 0 3px rgba(189, 80, 209, 0.25); }
#allow-form button { margin-top: 0.3rem; padding: 0.55rem 1.1rem; border: 0; border-radius: 999px; background: var(--brand); color: #fff;
  font: 600 0.9rem system-ui, sans-serif; cursor: pointer; }
#allow-form button:hover { background: var(--brand-strong); }
#allow-result { margin: 1rem 0 0; padding: 0.8rem; border: 1px solid var(--line); border-radius: 8px; background: var(--code); color: #d9cbe0;
  font: 0.8rem/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; white-space: pre-wrap; overflow-x: auto; }
#allow-result:empty { display: none; }
footer.version-footer { max-width: 64rem; margin: 0 auto; padding: 1.25rem; border-top: 1px solid var(--line); color: var(--muted); font-size: 0.8rem; text-align: center; }
footer.version-footer a { color: var(--muted); text-decoration: underline; }
</style>
</head>
<body>
<header class="top"><div class="inner">
<span class="wordmark">` + landingEyeSVG + `polypus</span><span class="pill">gateway</span>
<a class="docs" href="` + landingDocsURL + `">Docs</a>
</div></header>
<main>
<div class="layout">
<div class="col col-ops">
<div class="card hero"><img class="banner" src="` + bannerAssetPath + `" width="1536" height="753" alt="Polypus"></div>
<nav class="card" aria-label="Health and observability">
<h2>Health &amp; collectors</h2>
<ul>
{{range .OpsLinks}}<li><a href="{{.Href}}"><span class="title">{{.Title}}</span><p class="desc">{{.Description}}</p></a></li>
{{end}}
</ul>
</nav>
{{if .OTLPNote}}<section class="notes" aria-label="OpenTelemetry endpoints">
<h2>Notes</h2>
<pre class="note">{{.OTLPNote}}</pre>
</section>{{end}}
</div>
<div class="col col-models">
<nav class="card" aria-label="Models and APIs">
<h2>Models</h2>
<ul>
{{range .ModelLinks}}<li><a href="{{.Href}}"><span class="title">{{.Title}}</span><p class="desc">{{.Description}}</p></a></li>
{{end}}
</ul>
</nav>
<section class="allow">
<h2>Allow model (inventory check)</h2>
<form id="allow-form">
<label>Backend <input name="backend" autocomplete="off" required placeholder="cf_local"></label>
<label>Model <input name="model" autocomplete="off" required placeholder="@cf/..."></label>
<button type="submit">Add to allow list</button>
</form>
<pre id="allow-result"></pre>
</section>
</div>
</div>
</main>
<footer class="version-footer" aria-label="Gateway version">Polypus {{.Version}} · <a href="` + landingDocsURL + `">behaviorengineering.github.io/polypus</a></footer>
<script>
(function () {
  var form = document.getElementById('allow-form');
  var out = document.getElementById('allow-result');
  if (!form || !out) return;
  form.addEventListener('submit', function (e) {
    e.preventDefault();
    var fd = new FormData(form);
    var payload = { backend: String(fd.get('backend') || '').trim(), model: String(fd.get('model') || '').trim() };
    out.textContent = 'Sending…';
    fetch('/v1/admin/models/allow', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).then(function (res) {
      return res.text().then(function (text) {
        out.textContent = res.status + ' ' + text;
      });
    }).catch(function (err) {
      out.textContent = String(err);
    });
  });
})();
</script>
</body>
</html>`

var landingPageTemplate = template.Must(
	template.New("landing").Parse(landingPageTmpl),
)

func landingPageDataForRequest(r *http.Request, cfg config.RouterConfig) landingPageData {
	return landingPageData{
		ModelLinks: landingModelLinks(cfg),
		OpsLinks:   landingOpsLinks(r),
		OTLPNote:   landingOTLPNote(r),
		Version:    buildinfo.Version,
	}
}

func landingModelLinks(cfg config.RouterConfig) []landingLink {
	links := []landingLink{
		{
			Href:        "/v1/apis",
			Title:       "API catalog",
			Description: "Discovery index with model list URLs for each API surface.",
		},
		{
			Href:        pathOpenAIModels,
			Title:       "OpenAI models (enabled)",
			Description: "Chat, vision, embed, audio, and router models allowed on this gateway.",
		},
	}
	if cfg.EffectiveSystemOneBackend() != "" {
		links = append(links, landingLink{
			Href:        pathSystemOneModels,
			Title:       "SystemOne models",
			Description: "TypeSafe and JEV decider models (POST /v1/systemone).",
		})
	}
	return links
}

func landingOpsLinks(r *http.Request) []landingLink {
	links := []landingLink{
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
	}
	return append(links, landingObservabilityUILinks(r)...)
}

func landingHostPortURL(r *http.Request, port string) string {
	// Phoenix and HyperDX listen plaintext HTTP on these ports (Tailscale TCP forward).
	return "http://" + requestHostname(r) + ":" + port
}

func landingOTLPEndpointURL(r *http.Request, port string) string {
	// Homelab landing is HTTPS (Tailscale Serve); local make serve is HTTP.
	return requestScheme(r) + "://" + requestHostname(r) + ":" + port
}

// Ports of the local observability stack the landing page links to.
const (
	phoenixUIPort = "6006"
	hyperdxUIPort = "8080"
	otlpGRPCPort  = "4317"
	otlpHTTPPort  = "4318"
)

func landingObservabilityUILinks(r *http.Request) []landingLink {
	return []landingLink{
		{
			Href:        landingHostPortURL(r, phoenixUIPort) + "/",
			Title:       "Phoenix (Arize)",
			Description: "OpenInference LLM traces for chat and router spans.",
		},
		{
			Href:        landingHostPortURL(r, hyperdxUIPort) + "/",
			Title:       "HyperDX (OpenTelemetry)",
			Description: "APM traces and logs.",
		},
	}
}

func landingOTLPNote(r *http.Request) string {
	grpc := landingOTLPEndpointURL(r, otlpGRPCPort)
	httpURL := landingOTLPEndpointURL(r, otlpHTTPPort)
	return "OTEL_EXPORTER_OTLP_ENDPOINT\n" +
		"grpc: " + grpc + "\n" +
		"http: " + httpURL
}

// URL schemes a request can arrive on.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

func requestScheme(r *http.Request) string {
	if r == nil {
		return schemeHTTP
	}
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		if i := strings.Index(proto, ","); i >= 0 {
			proto = strings.TrimSpace(proto[:i])
		}
		if proto == schemeHTTPS || proto == schemeHTTP {
			return proto
		}
	}
	if r.TLS != nil {
		return schemeHTTPS
	}
	return schemeHTTP
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

func (g *gatewayHandler) serveLanding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := config.RouterConfig{}
	if g.router != nil {
		cfg = g.router.Registry().Config()
	}
	data := landingPageDataForRequest(r, cfg)
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

func (g *gatewayHandler) serveBannerWebP(w http.ResponseWriter, r *http.Request) {
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
