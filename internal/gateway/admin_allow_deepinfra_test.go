package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func deepInfraAllowBackendConfig(baseURL string) config.RouterConfig {
	syncTrue := true
	return config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"deepinfra": {
				ID:      "deepinfra",
				Remote:  true,
				BaseURL: baseURL,
				Auth:    config.BackendAuth{BearerEnv: "DEEPINFRA_TEST_KEY"},
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"google/gemma-3-27b-it"},
					Sync:            &syncTrue,
				},
			},
		},
	}
}

func TestAdminAllowDeepInfraFromLiveInventory(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("DEEPINFRA_TEST_KEY", "secret")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/openai/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":       "google/gemma-3-27b-it",
					"object":   "model",
					"owned_by": "deepinfra",
					"root":     "google/gemma-3-27b-it",
					"parent":   nil,
					"metadata": map[string]any{"tags": []string{"chat", "vision"}},
				},
				{
					"id":       "deepseek-ai/DeepSeek-V4-Flash-0731",
					"object":   "model",
					"owned_by": "deepinfra",
					"root":     "deepseek-ai/DeepSeek-V4-Flash-0731",
					"parent":   nil,
					"metadata": map[string]any{"tags": []string{"chat"}},
				},
			},
		})
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(deepInfraAllowBackendConfig(modelsSrv.URL + "/v1/openai"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	body := []byte(`{"backend":"deepinfra","model":"deepseek-ai/DeepSeek-V4-Flash-0731"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	b, _ := reg.Backend("deepinfra")
	if !b.IsModelAllowed("deepseek-ai/DeepSeek-V4-Flash-0731") {
		t.Fatal("expected in-memory allow for live-only slug")
	}
}

func TestAdminAllowDeepInfraInventoryMiss(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("DEEPINFRA_TEST_KEY", "secret")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/openai/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "google/gemma-3-27b-it", "object": "model"}},
		})
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(deepInfraAllowBackendConfig(modelsSrv.URL + "/v1/openai"))
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr, WithAdminStateDir(dir))
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader([]byte(`{"backend":"deepinfra","model":"not-on-deepinfra"}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
