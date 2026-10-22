package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestAdminAllowSuccessWithoutAuth(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "@cf/b", "object": "model"}},
		})
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(gatedAdminBackendConfig(modelsSrv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	body := []byte(`{"backend":"cf","model":"@cf/b"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp adminAllowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AlreadyAllowed {
		t.Fatal("expected new allow")
	}
	b, _ := reg.Backend("cf")
	if !b.IsModelAllowed("@cf/b") {
		t.Fatal("expected in-memory allow")
	}
}

func TestAdminAllowInventoryMiss(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "@cf/a", "object": "model"}},
		})
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(gatedAdminBackendConfig(modelsSrv.URL + "/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader([]byte(`{"backend":"cf","model":"@cf/missing"}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func gatedAdminBackendConfig(baseURL string) config.RouterConfig {
	return config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"cf": {
				ID:      "cf",
				BaseURL: baseURL,
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"@cf/a"},
				},
			},
		},
	}
}

func TestAdminAllowOverlayGetEmpty(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://example.invalid/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/models/allow", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp adminAllowOverlayResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Object != "allow_overlay" {
		t.Fatalf("object %q", resp.Object)
	}
	if len(resp.Backends) != 0 {
		t.Fatalf("backends %v", resp.Backends)
	}
}

func TestAdminAllowOverlayGetFile(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://example.invalid/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "models-allow-overlay.yaml")
	const overlayYAML = "backends:\n  cf:\n    allow:\n      - \"@cf/b\"\n"
	if err := os.WriteFile(overlayPath, []byte(overlayYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/models/allow", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp adminAllowOverlayResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Backends["cf"].Allow[0] != "@cf/b" {
		t.Fatalf("allow %v", resp.Backends["cf"].Allow)
	}
}

func TestAdminAllowOverlayGetIncludesYAMLDuplicate(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://example.invalid/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	overlayPath := filepath.Join(dir, "models-allow-overlay.yaml")
	const overlayYAML = "backends:\n  cf:\n    allow:\n      - \"@cf/a\"\n"
	if err := os.WriteFile(overlayPath, []byte(overlayYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/models/allow", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !bytes.Contains(rec.Body.Bytes(), []byte("@cf/a")) {
		t.Fatalf("expected overlay duplicate in body %s", body)
	}
}

func TestAdminAllowOverlayHead(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://example.invalid/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodHead, "/v1/admin/models/allow", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("body len %d", rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q", ct)
	}
}
