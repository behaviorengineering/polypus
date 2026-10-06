package gateway

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestAccessEmptyStoreOpen(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestAccessKeysPresentRequiresCredential(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	store, err := keys.Config{
		Path:  filepath.Join(dir, "admin-api-keys.json"),
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	gen, err := store.Generate("ops")
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET / status %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+gen.Key)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / with bearer status %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	basic := base64.StdEncoding.EncodeToString([]byte("user:" + gen.Key))
	req.Header.Set("Authorization", "Basic "+basic)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / with basic status %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Api-Key", gen.Key)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / with x-api-key status %d", rec.Code)
	}
}

func TestAccessHealthExemptWhenGated(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	store, err := keys.Config{
		Path:  filepath.Join(dir, "admin-api-keys.json"),
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Generate("ops"); err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))

	for _, path := range []string{"/health", "/health/backends", "/health/upstreams"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("%s should be exempt, got 401", path)
		}
	}
}

func TestAccessCorruptKeyStoreFailsClosed(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	keysPath := filepath.Join(dir, "admin-api-keys.json")
	if err := os.WriteFile(keysPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("corrupt store: health status %d want 503", rec.Code)
	}
}
