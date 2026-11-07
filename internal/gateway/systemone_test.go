package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// typesafeSystemOneConfig is scriptedRouterConfig plus "decider", a Typesafe
// backend that is the SystemOne default, at baseURL.
func typesafeSystemOneConfig(baseURL string) config.RouterConfig {
	cfg := scriptedRouterConfig()
	cfg.SystemOne = config.CapabilityBackend{Enabled: true, Default: "decider"}
	cfg.Backends["decider"] = config.BackendDef{
		ID:           "decider",
		Extension:    config.ExtensionTypesafe,
		BaseURL:      baseURL,
		Capabilities: []config.Capability{config.CapSystemOne},
	}
	return cfg
}

// postSystemOne sends one SystemOne decision for model to h.
func postSystemOne(t *testing.T, h http.Handler, model string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{
		"model": "`+model+`",
		"state": "Help! payouts failing for 3 days.",
		"questions": {"is_urgent": {"type": "noul", "instructions": "Does this convey urgency?"}}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSystemOneTypesafeGoesThroughBifrost(t *testing.T) {
	var dialled atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dialled.Add(1)
		http.Error(w, "the gateway must not dial Typesafe itself", http.StatusTeapot)
	}))
	t.Cleanup(upstream.Close)
	sr := &scriptedRouter{decision: func(context.Context, router.Call) ([]byte, error) {
		return []byte(`{"model":"jev-1.14.0","answers":{"is_urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":5}}`), nil
	}}
	h := newScriptedGateway(t, sr, typesafeSystemOneConfig(upstream.URL))

	rec := postSystemOne(t, h, "decider/typesafe/jev")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if dialled.Load() != 0 {
		t.Fatalf("the passthrough transport must never be dialled, hits=%d", dialled.Load())
	}
	calls := sr.recorded()
	if len(calls) != 1 || calls[0].BackendID != "decider" || calls[0].Model != "typesafe/jev" || calls[0].Timeout <= 0 {
		t.Fatalf("the decision must go through the router once with a budget: %+v", calls)
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["model"] != "decider/typesafe/jev" {
		t.Fatalf("the public model must replace the provider's: %v", resp["model"])
	}
}

func TestSystemOneUnknownBackendIsRejected(t *testing.T) {
	cfg := typesafeSystemOneConfig("http://127.0.0.1:9")
	// Config load refuses this; an injected config gets the same answer at request time.
	decider := cfg.Backends["decider"]
	decider.Extension = ""
	cfg.Backends["decider"] = decider
	sr := &scriptedRouter{}
	h := newScriptedGateway(t, sr, cfg)

	rec := postSystemOne(t, h, "decider/typesafe/jev")
	if rec.Code != http.StatusBadRequest || !jsonErrorCode(t, rec.Body.Bytes(), string(derrors.CodeFailedPrecondition)) {
		t.Fatalf("a systemone backend with no decision route fails closed: %d %s", rec.Code, rec.Body.String())
	}
	if len(sr.recorded()) != 0 {
		t.Fatal("nothing may be dialled")
	}
}

func TestSystemOneCloudflare(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	t.Setenv("CF_AI_API_KEY", "test-key")

	var gotPath string
	var gotBody []byte
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"errors":  []any{},
			"result": map[string]any{
				"model":   "jev-1.13.0",
				"answers": map[string]any{"is_urgent": map[string]any{"type": "noul", "noul": 0.95}},
				"usage":   map[string]int{"input_tokens": 42, "output_tokens": 8},
			},
		})
	}))
	t.Cleanup(cf.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
systemone_backend:
  enabled: true
  default: cf_local
tts_backend:
  enabled: true
  default: cf_local
stt_backend:
  enabled: true
  default: cf_local
proxy_backend:
  enabled: true
  default: cf_local
backends:
  cf_local:
    remote: true
    extension: cloudflare
    base_url: %s/client/v4/accounts/acct-test/ai/v1
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [systemone, tts, stt, voices]
    models:
      allow:
        - typesafe/jev
`, cf.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	opts := config.ServeOptions{BackendURL: cf.URL}
	fake := newFakeRouter(t, opts)
	handler := newTestGateway(t, opts, fake)

	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{
		"model": "cf_local/typesafe/jev",
		"state": "Help! payouts failing.",
		"questions": {
			"is_urgent": {
				"type": "noul",
				"instructions": "Does this convey urgency?",
				"criteria": {"true": "time-sensitive", "false": "no urgency"}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.HasSuffix(gotPath, "/run") || strings.Contains(gotPath, "/run/") {
		t.Fatalf("CF path %q", gotPath)
	}
	var forwarded map[string]json.RawMessage
	if err := json.Unmarshal(gotBody, &forwarded); err != nil {
		t.Fatal(err)
	}
	if string(forwarded["model"]) != `"typesafe/jev"` {
		t.Fatalf("CF model %s", forwarded["model"])
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(forwarded["input"], &input); err != nil {
		t.Fatal(err)
	}
	if _, ok := input["model"]; ok {
		t.Fatal("model should not be nested under input")
	}
	if _, ok := input["state"]; !ok {
		t.Fatal("state missing from input")
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["model"] != "cf_local/typesafe/jev" {
		t.Fatalf("response model %v", resp["model"])
	}
}

func TestSystemOneAllowReject(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
systemone_backend:
  enabled: true
  default: decider
tts_backend:
  enabled: true
  default: decider
stt_backend:
  enabled: true
  default: decider
proxy_backend:
  enabled: true
  default: decider
backends:
  decider:
    extension: typesafe
    base_url: %s
    capabilities: [systemone, tts, stt, voices]
    models:
      allow:
        - typesafe/jev
`, upstream.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	opts := config.ServeOptions{BackendURL: upstream.URL}
	handler := newTestGateway(t, opts, newFakeRouter(t, opts))

	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{
		"model": "decider/other-model",
		"state": "x",
		"questions": {"q": {"type": "noul", "instructions": "?"}}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model_not_allowed") {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestSystemOneMissingState(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	dir := t.TempDir()
	content := fmt.Sprintf(`
systemone_backend:
  enabled: true
  default: decider
tts_backend:
  enabled: true
  default: decider
stt_backend:
  enabled: true
  default: decider
proxy_backend:
  enabled: true
  default: decider
backends:
  decider:
    extension: typesafe
    base_url: %s
    capabilities: [systemone, tts, stt, voices]
`, upstream.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	opts := config.ServeOptions{BackendURL: upstream.URL}
	handler := newTestGateway(t, opts, newFakeRouter(t, opts))

	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{
		"questions": {"q": {"type": "noul", "instructions": "?"}}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSystemOneModelRewriteKeepsLargeIntegersExact(t *testing.T) {
	out, err := rewriteSystemOneResponseModel([]byte(`{"model":"x","n":9007199254740993}`), "public")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"n":9007199254740993`) || !strings.Contains(string(out), `"model":"public"`) {
		t.Fatalf("rewrite: %s", out)
	}
}
