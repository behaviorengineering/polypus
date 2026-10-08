package gateway

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/media"
)

func newLandingTestHandler(t *testing.T) http.Handler {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestLandingPageHTML(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:1320"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type: %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="layout"`,
		`class="col col-models"`,
		`class="col col-ops"`,
		"/health/backends",
		"/health/upstreams",
		"/v1/apis",
		"/v1/apis/openai/models",
		`href="/phoenix/"`,
		`href="/hyperdx/"`,
		"Probes each configured backend",
		"Circuit-breaker state",
		"Discovery index with model list URLs",
		"OpenInference LLM traces for chat and router spans",
		"APM traces and logs",
		bannerAssetPath,
		`id="allow-form"`,
		`name="backend"`,
		`name="model"`,
		"/v1/admin/models/allow",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	iOps := strings.Index(body, `class="col col-ops"`)
	iBanner := strings.Index(body, `class="banner"`)
	iHealth := strings.Index(body, "Health &amp; collectors")
	if iOps < 0 || iBanner < 0 || iHealth < 0 || iOps >= iBanner || iBanner >= iHealth {
		t.Fatalf("banner should be at top of left ops column:\n%s", body)
	}
}

func TestLandingHEAD(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodHead, "/", nil)
	req.Host = "127.0.0.1:1320"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content-type: %q", ct)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body len: got %d", rec.Body.Len())
	}
}

func TestLandingMethodNotAllowed(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestLandingWrongAssetPath(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/media/other.webp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestLandingObservabilityUsesPathProxies(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "polypus.example:1320"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, ":6006") || strings.Contains(body, ":8080") {
		t.Fatalf("expected path-based observability links, body:\n%s", body)
	}
	if !strings.Contains(body, `href="/phoenix/"`) || !strings.Contains(body, `href="/hyperdx/"`) {
		t.Fatalf("missing path proxy hrefs, body:\n%s", body)
	}
}

func TestBannerWebP(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, bannerAssetPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Fatalf("content-type: %q", ct)
	}
	if rec.Body.Len() != len(media.BannerWebP) {
		t.Fatalf("body len: got %d want %d", rec.Body.Len(), len(media.BannerWebP))
	}
}

func TestBannerHEAD(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodHead, bannerAssetPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Fatalf("content-type: %q", ct)
	}
	if cl := rec.Header().Get("Content-Length"); cl == "" {
		t.Fatal("missing Content-Length")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body len: got %d", rec.Body.Len())
	}
}

func TestBannerMethodNotAllowed(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodPost, bannerAssetPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status: got %d", rec.Code)
	}
}

func TestHealthUnchangedAfterLanding(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type: %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestRequestHostnameSanitization(t *testing.T) {
	t.Run("nil request", func(t *testing.T) {
		if got := requestHostname(nil); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("empty host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http:///", nil)
		req.Host = ""
		req.URL.Host = ""
		if got := requestHostname(req); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("unsafe host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "evil\nhost:1320"
		if got := requestHostname(req); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("strip port", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "gateway.example.com:1320"
		if got := requestHostname(req); got != "gateway.example.com" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("ipv6 bracket fallback", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "[::1]:1320"
		if got := requestHostname(req); got != "127.0.0.1" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestRequestScheme(t *testing.T) {
	t.Run("forwarded proto first value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-Proto", "https, http")
		if got := requestScheme(req); got != "https" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("default http", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if got := requestScheme(req); got != "http" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("tls", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.TLS = &tls.ConnectionState{}
		if got := requestScheme(req); got != "https" {
			t.Fatalf("got %q", got)
		}
	})
}
