package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/assets"
	"github.com/behaviorengineering/polypus/internal/gateway/landing"
)

func newLandingTestHandler(t *testing.T) http.Handler {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := newHandlerForTest(t, config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Close() })
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
	if strings.Contains(body, `href="http://127.0.0.1:4317"`) || strings.Contains(body, `href="http://127.0.0.1:4318"`) {
		t.Fatalf("OTLP endpoints must be notes, not links, body:\n%s", body)
	}
	for _, want := range []string{
		`class="layout"`,
		`class="col col-models"`,
		`class="col col-ops"`,
		"/health/backends",
		"/health/upstreams",
		"/v1/apis",
		"/v1/apis/openai/models",
		`href="http://127.0.0.1:6006/"`,
		`href="http://127.0.0.1:8080/"`,
		`class="notes"`,
		"Notes",
		"Probes each configured backend",
		"Circuit-breaker state",
		"Discovery index with model list URLs",
		"OpenInference LLM traces for chat and router spans",
		"APM traces and logs",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"grpc: http://127.0.0.1:4317",
		"http: http://127.0.0.1:4318",
		landing.BannerPath,
		`id="allow-form"`,
		`name="backend"`,
		`name="model"`,
		"/v1/admin/models/allow",
		`class="version-footer"`,
		"Polypus dev",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	iBanner := strings.Index(body, `class="banner"`)
	iAllow := strings.Index(body, `id="allow-form"`)
	iDivider := strings.Index(body, `class="divider"`)
	iModels := strings.Index(body, `class="col col-models"`)
	iOps := strings.Index(body, `class="col col-ops"`)
	if iBanner < 0 || iAllow < 0 || iDivider < 0 || iModels < 0 || iOps < 0 ||
		iBanner >= iAllow || iAllow >= iDivider || iDivider >= iModels || iModels >= iOps {
		t.Fatalf("banner and allow form should sit above the divider, then models and ops columns:\n%s", body)
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

func TestLandingObservabilityUsesHostPorts(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "polypus.example:1320"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, `href="/phoenix/"`) || strings.Contains(body, `href="/hyperdx/"`) {
		t.Fatalf("expected port-based observability links, body:\n%s", body)
	}
	if !strings.Contains(body, `href="http://polypus.example:6006/"`) || !strings.Contains(body, `href="http://polypus.example:8080/"`) {
		t.Fatalf("missing host-port hrefs, body:\n%s", body)
	}
	if strings.Contains(body, `href="https://polypus.example:4317"`) || strings.Contains(body, `href="https://polypus.example:4318"`) {
		t.Fatalf("OTLP endpoints must be notes, not links, body:\n%s", body)
	}
	if !strings.Contains(body, "grpc: https://polypus.example:4317") {
		t.Fatalf("missing OTLP gRPC https scheme in note, body:\n%s", body)
	}
	if !strings.Contains(body, "http: https://polypus.example:4318") {
		t.Fatalf("missing OTLP HTTP https scheme in note, body:\n%s", body)
	}
}

func TestBannerWebP(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, landing.BannerPath, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Fatalf("content-type: %q", ct)
	}
	if rec.Body.Len() != len(assets.BannerWebP()) {
		t.Fatalf("body len: got %d want %d", rec.Body.Len(), len(assets.BannerWebP()))
	}
}

func TestBannerHEAD(t *testing.T) {
	handler := newLandingTestHandler(t)

	req := httptest.NewRequest(http.MethodHead, landing.BannerPath, nil)
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

	req := httptest.NewRequest(http.MethodPost, landing.BannerPath, nil)
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

func TestLandingHrefsMatchRouteTable(t *testing.T) {
	pairs := [][2]string{
		{landing.HrefAPIs, pathAPIs},
		{landing.HrefOpenAIModels, pathOpenAIModels},
		{landing.HrefSystemOneModels, pathSystemOneModels},
		{landing.HrefAdminModelsAllow, pathAdminModelsAllow},
		{landing.HrefHealthBackends, pathHealthBackends},
		{landing.HrefHealthUpstreams, pathHealthUpstreams},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("landing href %q does not match route %q", p[0], p[1])
		}
	}
}
