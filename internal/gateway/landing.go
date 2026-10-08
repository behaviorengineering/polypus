package gateway

import (
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/media"
)

const bannerAssetPath = "/media/banner.webp"

// Matte sampled from media/banner.webp so the page matches the logo asset (not #000).
const landingPageBackground = "#010302"

type landingLink struct {
	Href        string
	Title       string
	Description string
}

type landingPageData struct {
	ModelLinks []landingLink
	OpsLinks   []landingLink
}

const landingPageTmpl = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Polypus</title>
<style>
html, body { margin: 0; min-height: 100%; background: ` + landingPageBackground + `; color: #e8e8e8; font-family: system-ui, sans-serif; }
main { max-width: 56rem; margin: 0 auto; padding: 2rem 1.25rem 3rem; }
.layout { display: grid; grid-template-columns: 1fr 1fr; gap: 2rem 2.5rem; align-items: start; }
@media (max-width: 720px) { .layout { grid-template-columns: 1fr; } }
.col { text-align: left; min-width: 0; }
img.banner { display: block; max-width: min(100%, 28rem); height: auto; margin: 0 0 2rem; }
nav h2 { font-size: 0.75rem; font-weight: 600; letter-spacing: 0.06em; text-transform: uppercase; color: #888; margin: 0 0 0.75rem; }
ul { list-style: none; padding: 0; margin: 0; }
li { margin: 0 0 1.25rem; }
a { color: #7eb8ff; text-decoration: none; font-weight: 600; }
a:hover { text-decoration: underline; }
p.desc { margin: 0.35rem 0 0; font-size: 0.9rem; color: #a8a8a8; line-height: 1.45; }
section.allow { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #333; }
section.allow h2 { font-size: 1rem; margin: 0 0 1rem; font-weight: 600; text-transform: none; letter-spacing: normal; color: #e8e8e8; }
#allow-form label { display: block; margin-bottom: 0.75rem; font-size: 0.9rem; }
#allow-form input { display: block; width: 100%; margin-top: 0.25rem; padding: 0.45rem 0.5rem; box-sizing: border-box; background: #111; border: 1px solid #444; color: #e8e8e8; border-radius: 4px; }
#allow-form button { margin-top: 0.5rem; padding: 0.5rem 1rem; background: #1a3a5c; color: #e8e8e8; border: none; border-radius: 4px; cursor: pointer; font-weight: 600; }
#allow-form button:hover { background: #254a70; }
#allow-result { margin-top: 1rem; padding: 0.75rem; background: #111; border: 1px solid #333; font-size: 0.8rem; overflow-x: auto; white-space: pre-wrap; color: #c8c8c8; min-height: 2rem; }
</style>
</head>
<body>
<main>
<div class="layout">
<div class="col col-ops">
<img class="banner" src="` + bannerAssetPath + `" width="1536" height="753" alt="Polypus">
<nav aria-label="Health and observability">
<h2>Health &amp; collectors</h2>
<ul>
{{range .OpsLinks}}<li><a href="{{.Href}}">{{.Title}}</a><p class="desc">{{.Description}}</p></li>
{{end}}
</ul>
</nav>
</div>
<div class="col col-models">
<nav aria-label="Models and APIs">
<h2>Models</h2>
<ul>
{{range .ModelLinks}}<li><a href="{{.Href}}">{{.Title}}</a><p class="desc">{{.Description}}</p></li>
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
		OpsLinks:   landingOpsLinks(cfg),
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
			Href:        "/v1/apis/openai/models",
			Title:       "OpenAI models (enabled)",
			Description: "Chat, vision, embed, audio, and router models allowed on this gateway.",
		},
	}
	if cfg.EffectiveSystemOneBackend() != "" {
		links = append(links, landingLink{
			Href:        "/v1/apis/systemone/models",
			Title:       "SystemOne models",
			Description: "TypeSafe and JEV decider models (POST /v1/systemone).",
		})
	}
	return links
}

func landingOpsLinks(cfg config.RouterConfig) []landingLink {
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
	return append(links, landingObservabilityUILinks(cfg)...)
}

func landingObservabilityUILinks(cfg config.RouterConfig) []landingLink {
	if len(cfg.UIProxies) > 0 {
		out := make([]landingLink, 0, len(cfg.UIProxies))
		for _, p := range cfg.UIProxies {
			out = append(out, landingLink{
				Href:        p.Path + "/",
				Title:       p.Title,
				Description: p.Description,
			})
		}
		return out
	}
	return []landingLink{
		{
			Href:        "/phoenix/",
			Title:       "Phoenix (Arize)",
			Description: "OpenInference LLM traces for chat and router spans.",
		},
		{
			Href:        "/hyperdx/",
			Title:       "HyperDX (OpenTelemetry)",
			Description: "APM traces and logs behind the gateway path proxy.",
		},
	}
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
