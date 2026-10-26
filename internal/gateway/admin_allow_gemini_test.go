package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/gemini"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

func geminiAllowBackendConfig() config.RouterConfig {
	syncTrue := true
	return config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"gemini_studio": {
				ID:        "gemini_studio",
				Remote:    true,
				Extension: config.ExtensionGemini,
				Auth:      config.BackendAuth{BearerEnv: "GEMINI_TEST_KEY"},
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"gemini-2.5-flash"},
					Sync:            &syncTrue,
				},
			},
		},
	}
}

func geminiTestClientGet(srvURL string) geminiClientGet {
	return func(b config.BackendDef) (*gemini.Client, error) {
		return gemini.NewClientWithListURL(srvURL, "secret")
	}
}

func TestAdminAllowGeminiFromLiveInventory(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("GEMINI_TEST_KEY", "secret")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gemma-4-26b-a4b-it","object":"model"},{"id":"gemini-2.5-flash","object":"model"}]}`))
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(geminiAllowBackendConfig())
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr,
		WithAdminStateDir(dir),
		withGeminiClientGet(geminiTestClientGet(modelsSrv.URL)),
	)
	body := []byte(`{"backend":"gemini_studio","model":"gemma-4-26b-a4b-it"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	b, _ := reg.Backend("gemini_studio")
	if !b.IsModelAllowed("gemma-4-26b-a4b-it") {
		t.Fatal("expected in-memory allow for live-only slug")
	}
}

func TestAdminAllowGeminiInventoryMiss(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("GEMINI_TEST_KEY", "secret")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gemini-2.5-flash","object":"model"}]}`))
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(geminiAllowBackendConfig())
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	dir := t.TempDir()
	h := newTestGateway(t, config.ServeOptions{}, fr,
		WithAdminStateDir(dir),
		withGeminiClientGet(geminiTestClientGet(modelsSrv.URL)),
	)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader([]byte(`{"backend":"gemini_studio","model":"not-in-google-catalog"}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestGeminiInventorySyncFalseSynthetic(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("GEMINI_TEST_KEY", "secret")
	syncFalse := false
	cfg := config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"gemini_studio": {
				ID:        "gemini_studio",
				Remote:    true,
				Extension: config.ExtensionGemini,
				Auth:      config.BackendAuth{BearerEnv: "GEMINI_TEST_KEY"},
				Models: &config.BackendModels{
					AllowConfigured: true,
					Allow:           []string{"gemini-2.5-flash"},
					Sync:            &syncFalse,
				},
			},
		},
	}
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("sync false must not call upstream list")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	t.Cleanup(modelsSrv.Close)
	reg, err := router.NewRegistry(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fr := &fakeRouter{reg: reg}
	h := newTestGateway(t, config.ServeOptions{}, fr,
		withGeminiClientGet(geminiTestClientGet(modelsSrv.URL)),
	)
	req := httptest.NewRequest(http.MethodGet, "/v1/apis/openai/models?view=inventory", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	foundLiveOnly := false
	for _, m := range payload.Data {
		if m.ID == "gemini_studio/gemma-4-26b-a4b-it" {
			foundLiveOnly = true
		}
	}
	if foundLiveOnly {
		t.Fatal("sync false inventory must not include live-only slug")
	}
	dir := t.TempDir()
	h2 := newTestGateway(t, config.ServeOptions{}, fr,
		WithAdminStateDir(dir),
		withGeminiClientGet(geminiTestClientGet(modelsSrv.URL)),
	)
	allowReq := httptest.NewRequest(http.MethodPost, "/v1/admin/models/allow", bytes.NewReader([]byte(`{"backend":"gemini_studio","model":"gemma-4-26b-a4b-it"}`)))
	allowRec := httptest.NewRecorder()
	h2.ServeHTTP(allowRec, allowReq)
	if allowRec.Code != http.StatusBadRequest {
		t.Fatalf("sync false allow live-only slug: status %d body %s", allowRec.Code, allowRec.Body.String())
	}
}
