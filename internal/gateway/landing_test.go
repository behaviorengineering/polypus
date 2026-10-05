package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/media"
)

func TestLandingPageHTML(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

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
		"/health",
		"/health/backends",
		"/health/upstreams",
		"/v1/models",
		"http://127.0.0.1:6006/",
		"http://127.0.0.1:8080/",
		"Gateway liveness JSON (no upstream dials).",
		"OpenAI-compatible catalog of models enabled on this gateway.",
		"OpenInference LLM traces for chat and router spans",
		"App traces and logs",
		bannerAssetPath,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func TestLandingSiblingURLsRespectForwardedProto(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "polypus.example:1320"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "https://polypus.example:6006/") {
		t.Fatalf("expected https sibling phoenix link, body:\n%s", body)
	}
	if !strings.Contains(body, "https://polypus.example:8080/") {
		t.Fatalf("expected https sibling hyperdx link, body:\n%s", body)
	}
}

func TestBannerWebP(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

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

func TestHealthUnchangedAfterLanding(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

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
	if got := requestHostname(nil); got != "127.0.0.1" {
		t.Fatalf("nil request: got %q", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "evil\nhost:1320"
	if got := requestHostname(req); got != "127.0.0.1" {
		t.Fatalf("unsafe host: got %q", got)
	}
}
