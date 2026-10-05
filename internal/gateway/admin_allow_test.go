package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/admin/keys"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func TestAdminAllowUnauthorized(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	reg, err := router.NewRegistry(gatedAdminBackendConfig("http://127.0.0.1:9/v1"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader([]byte(`{"backend":"cf","model":"@cf/x"}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestAdminAllowSuccess(t *testing.T) {
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
	keysPath := filepath.Join(dir, "admin-api-keys.json")
	store, err := keys.Config{
		Path:  keysPath,
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}.CreateStore()
	if err != nil {
		t.Fatal(err)
	}
	gen, err := store.Generate("t")
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	body := []byte(`{"backend":"cf","model":"@cf/b"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+gen.Key)
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
