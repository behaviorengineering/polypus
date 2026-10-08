package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestUIProxyStripsPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/assets/app.js" {
			t.Fatalf("path: got %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
ui_proxies:
  - path: /phoenix
    url: ` + upstream.URL + `
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", cfgPath)
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/phoenix/assets/app.js", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestLandingUsesHostPortsWhenUIProxiesConfigured(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `tts_backend:
  enabled: true
  default: mlx_local
stt_backend:
  enabled: true
  default: mlx_local
proxy_backend:
  enabled: true
  default: mlx_local
ui_proxies:
  - path: /phoenix
    url: http://127.0.0.1:6006
    title: Phoenix (Arize)
  - path: /hyperdx
    url: http://127.0.0.1:8080
    title: HyperDX
backends:
  mlx_local:
    base_url: http://127.0.0.1:1322
    capabilities: [tts, stt, voices]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", cfgPath)
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:1320"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, `href="/phoenix/"`) || strings.Contains(body, `href="/hyperdx/"`) {
		t.Fatalf("expected port links even with ui_proxies, body:\n%s", body)
	}
	if !strings.Contains(body, `href="http://127.0.0.1:6006/"`) || !strings.Contains(body, `href="http://127.0.0.1:8080/"`) {
		t.Fatalf("missing host-port hrefs, body:\n%s", body)
	}
}

func TestUIProxyPreservesPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/hyperdx/search" {
			t.Fatalf("path: got %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	proxy := newUIReverseProxy("/hyperdx", mustParseURL(upstream.URL), false)
	req := httptest.NewRequest(http.MethodGet, "/hyperdx/search", nil)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestUIProxyForwardsUpgradeHeader(t *testing.T) {
	var sawUpgrade bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUpgrade = strings.EqualFold(r.Header.Get("Connection"), "Upgrade")
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	t.Cleanup(upstream.Close)

	proxy := newUIReverseProxy("/phoenix", mustParseURL(upstream.URL), true)
	req := httptest.NewRequest(http.MethodGet, "/phoenix/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)
	_, _ = io.ReadAll(rec.Result().Body)
	if !sawUpgrade {
		t.Fatal("expected Connection Upgrade forwarded")
	}
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
