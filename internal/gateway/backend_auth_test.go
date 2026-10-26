package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestModelListNeverForwardsCallerAuthToARemote(t *testing.T) {
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	t.Cleanup(srv.Close)
	const unsetBearerEnv = "POLYPUS_TEST_UNSET_BEARER" //nolint:gosec // G101: an env var name, not a credential
	t.Setenv(unsetBearerEnv, "")
	b := config.BackendDef{
		ID: "remote_x", Remote: true, BaseURL: srv.URL + "/v1",
		Auth: config.BackendAuth{BearerEnv: unsetBearerEnv},
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer ppk.caller.secret")
	if _, err := testShared.fetchBackendModelsErr(r.Context(), r.Header.Get("Authorization"), b); err == nil {
		t.Fatal("an unresolved backend credential must fail the fetch")
	}
	if got, _ := seen.Load().(string); got != "" {
		t.Fatalf("caller Authorization reached the remote backend: %q", got)
	}
}
