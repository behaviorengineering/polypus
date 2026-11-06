package router

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// typesafeTestKeyEnv names the test credential of the Typesafe backend.
const typesafeTestKeyEnv = "TYPESAFE_TEST_KEY" //nolint:gosec // G101: an env var name, not a credential

const systemOneTestBody = `{"model":"typesafe/jev","state":"Help! payouts failing.","questions":{"is_urgent":{"type":"noul","instructions":"Does this convey urgency?"}}}`

// typesafeClient is a real Bifrost client whose only backend, "jev", is a Typesafe
// extension backend at baseURL with a bearer credential.
func typesafeClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv(typesafeTestKeyEnv, "typesafe-secret-key-for-tests")
	client, err := NewClient(t.Context(), config.RouterConfig{
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"jev": {
				ID:           "jev",
				Remote:       true,
				Extension:    config.ExtensionTypesafe,
				BaseURL:      baseURL,
				Auth:         config.BackendAuth{BearerEnv: typesafeTestKeyEnv},
				Capabilities: []config.Capability{config.CapSystemOne},
			},
		},
	}, WithLogger(testLogger()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestDecisionRawTypesafe(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"jev-1.14.0","answers":{"is_urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	t.Cleanup(srv.Close)
	// A configured /v1 suffix is trimmed: the provider appends /v1/systemone itself.
	client := typesafeClient(t, srv.URL+"/v1")

	raw, err := client.SystemOneDecisionRaw(t.Context(), Call{BackendID: "jev", Model: "jev-1.14.0", Body: []byte(systemOneTestBody), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/systemone" {
		t.Fatalf("path %q", gotPath)
	}
	if gotAuth != "Bearer typesafe-secret-key-for-tests" {
		t.Fatalf("the configured bearer must be sent, got %q", gotAuth)
	}
	if gotBody["model"] != "jev-1.14.0" {
		t.Fatalf("the downstream model must be sent: %v", gotBody["model"])
	}
	var resp map[string]any
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if resp["model"] != "jev-1.14.0" || resp["answers"] == nil {
		t.Fatalf("the native Typesafe answer must come back: %s", raw)
	}
}

func TestDecisionRawWithoutTimeoutFailsClosed(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	client := typesafeClient(t, srv.URL)

	_, err := client.SystemOneDecisionRaw(t.Context(), Call{BackendID: "jev", Model: "jev", Body: []byte(systemOneTestBody)})
	if derrors.CodeOf(err) != derrors.CodeFailedPrecondition {
		t.Fatalf("a decision with no timeout and no deadline must fail closed, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("nothing may be sent")
	}
}
