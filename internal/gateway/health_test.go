package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/cloudflare"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
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

func TestBackendHealthHonorsRequestCancel(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	block := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(block)
		backend.Close()
	})

	handler, err := NewHandler(config.ServeOptions{BackendURL: backend.URL})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/health/backends", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d body %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"degraded"`) {
		t.Fatalf("body: %q", rec.Body.String())
	}
}

func TestProbeCloudflareCredentialsSkipsLocal(t *testing.T) {
	cfg := config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"mlx_local": {
				ID:           "mlx_local",
				BaseURL:      "http://127.0.0.1:1322",
				Capabilities: []config.Capability{config.CapTTS},
			},
		},
	}
	calls := 0
	getCF := func(def config.BackendDef) (*cloudflare.Client, error) {
		calls++
		return nil, nil
	}
	if err := probeCloudflareCredentials(context.Background(), cfg, getCF); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("cfGet calls=%d", calls)
	}
}

func TestProbeCloudflareCredentialsFailsOnPing401(t *testing.T) {
	const sentinel = "sentinel-token-not-for-logs"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/ai/models/search") {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("CF_AI_API_KEY", sentinel)
	cfg := config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Remote:       true,
				Extension:    config.ExtensionCloudflare,
				BaseURL:      srv.URL + "/client/v4/accounts/acct/ai/v1",
				Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	getCF := func(def config.BackendDef) (*cloudflare.Client, error) {
		return cloudflare.NewClient(def)
	}
	err := probeCloudflareCredentials(context.Background(), cfg, getCF)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, derrors.ErrUnauthorized) {
		t.Fatalf("expected unauthorized: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "cf_local") {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(msg, sentinel) {
		t.Fatalf("error leaked token: %v", err)
	}
}

func TestProbeCloudflareCredentialsFailsOnGetError(t *testing.T) {
	cfg := config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"cf_local": {
				ID:           "cf_local",
				Remote:       true,
				Extension:    config.ExtensionCloudflare,
				BaseURL:      "https://api.cloudflare.com/client/v4/accounts/acct/ai/v1",
				Auth:         config.BackendAuth{BearerEnv: "CF_AI_API_KEY"},
				Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	getCF := func(def config.BackendDef) (*cloudflare.Client, error) {
		return nil, fmt.Errorf("bad creds")
	}
	err := probeCloudflareCredentials(context.Background(), cfg, getCF)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cf_local") {
		t.Fatalf("error %v", err)
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
