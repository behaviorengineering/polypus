package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/anthropic"
	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// anthropicInventoryKeyEnv names the env var that holds the test key.
const anthropicInventoryKeyEnv = "ANTHROPIC_TEST_KEY"

func TestAnthropicInventoryListsPrefixedModels(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv(anthropicInventoryKeyEnv, "secret")
	modelsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-sonnet-5-5"},{"id":"claude-haiku-5-5"}],"has_more":false}`))
	}))
	t.Cleanup(modelsSrv.Close)

	syncTrue := true
	reg, err := router.NewRegistry(config.RouterConfig{
		Backends: map[string]config.BackendDef{
			"claude_team": {
				ID:           "claude_team",
				Remote:       true,
				Extension:    config.ExtensionAnthropic,
				Auth:         config.BackendAuth{BearerEnv: anthropicInventoryKeyEnv},
				Capabilities: []config.Capability{config.CapChat},
				Models:       &config.BackendModels{Sync: &syncTrue},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := newTestGateway(t, config.ServeOptions{}, &fakeRouter{reg: reg},
		WithAdminStateDir(t.TempDir()),
		withAnthropicClientGet(func(config.BackendDef) (*anthropic.Client, error) {
			return anthropic.NewClientWithListURL(modelsSrv.URL, "secret")
		}),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, m := range list.Data {
		got[m.ID] = true
	}
	if !got["claude_team/claude-sonnet-5-5"] || !got["claude_team/claude-haiku-5-5"] {
		t.Fatalf("models: %s", rec.Body.String())
	}
}
