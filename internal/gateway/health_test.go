package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func startMockSwitchyard(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"routed"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("POLYPUS_SWITCHYARD_BASE_URL", srv.URL)
	return srv
}

func TestBackendHealthOKWhenBackendUp(t *testing.T) {
	startMockSwitchyard(t)
	t.Setenv("POLYPUS_SWITCHYARD", "1")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("body: %q", body)
	}
	if !strings.Contains(body, `"id":"switchyard"`) {
		t.Fatalf("missing switchyard probe: %q", body)
	}
	if !strings.Contains(body, `"ok":true`) {
		t.Fatalf("body: %q", body)
	}
}

func TestBackendHealthDegradedWhenBackendDown(t *testing.T) {
	startMockSwitchyard(t)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"degraded"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestHealthIncludesSwitchyardURL(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "1")
	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"switchyard"`) {
		t.Fatalf("body: %s", rec.Body.String())
	}
}

func TestHealthOmitsSwitchyardWhenDisabled(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	handler, err := NewHandler(config.ServeOptions{BackendURL: "http://127.0.0.1:1322"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"switchyard"`) {
		t.Fatalf("body should omit switchyard: %s", rec.Body.String())
	}
}

func TestBackendHealthSkipsSwitchyardWhenDisabled(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"id":"switchyard"`) {
		t.Fatalf("switchyard should be omitted: %q", rec.Body.String())
	}
}

func TestProbeSwitchyardRejectsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	if err := probeSwitchyard(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for 404 health")
	}
}
