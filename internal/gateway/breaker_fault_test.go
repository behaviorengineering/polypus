package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/behaviorengineering/polypus/internal/clients/gemini"
	"github.com/behaviorengineering/polypus/internal/config"
	derrors "github.com/behaviorengineering/polypus/internal/errors"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
	"github.com/behaviorengineering/polypus/internal/gateway/upstream"
)

// timedOutRouter answers every chat with a deadline error, as the router does when
// the hop budget runs out before the backend answers.
func timedOutRouter() *scriptedRouter {
	return &scriptedRouter{chat: func(context.Context, router.Call) ([]byte, error) {
		return nil, derrors.New(derrors.CodeTimeout, "router.bifrost", "deadline exceeded")
	}}
}

// postChatWithTimeout sends one chat request with an X-Polypus-Timeout header.
func postChatWithTimeout(t *testing.T, h http.Handler, timeout string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"leaf/m","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(config.TimeoutHeader, timeout)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCallerShortTimeoutDoesNotOpenTheBreaker(t *testing.T) {
	sr := timedOutRouter()
	h := newScriptedGateway(t, sr, scriptedRouterConfig())
	attempts := int(upstream.ConsecutiveFailuresTrip) + 2
	for i := range attempts {
		// 100ms is under the leaf's 200ms chat budget, so the deadline is the caller's.
		if rec := postChatWithTimeout(t, h, "100ms"); rec.Code != http.StatusGatewayTimeout {
			t.Fatalf("attempt %d: status %d, want 504: %s", i, rec.Code, rec.Body.String())
		}
	}
	if got := len(sr.recorded()); got != attempts {
		t.Fatalf("a caller's short budget must not open the breaker: router calls=%d, want %d", got, attempts)
	}
}

func TestGatewayHopTimeoutOpensTheBreaker(t *testing.T) {
	for _, header := range []string{"", "1s"} {
		sr := timedOutRouter()
		h := newScriptedGateway(t, sr, scriptedRouterConfig())
		for range upstream.ConsecutiveFailuresTrip {
			postChatWithTimeout(t, h, header)
		}
		rec := postChatWithTimeout(t, h, header)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("header %q: a backend past the gateway's own budget must open the breaker, got %d %s", header, rec.Code, rec.Body.String())
		}
		if got := len(sr.recorded()); got != int(upstream.ConsecutiveFailuresTrip) {
			t.Fatalf("header %q: router calls=%d, want %d", header, got, upstream.ConsecutiveFailuresTrip)
		}
	}
}

func TestCallerBudgetMarksOnlyAShorterBudget(t *testing.T) {
	ctx := context.Background()
	if !config.CallerBudgetFrom(callerBudget(ctx, 100, 200)) {
		t.Fatal("a shorter caller budget must be marked")
	}
	if config.CallerBudgetFrom(callerBudget(ctx, 200, 200)) || config.CallerBudgetFrom(callerBudget(ctx, 300, 200)) {
		t.Fatal("an equal or longer budget is the gateway's own")
	}
}

// writeSystemOneCloudflareConfig points POLYPUS_CONFIG at a Cloudflare SystemOne
// backend on baseURL whose key is read from CF_AI_API_KEY.
func writeSystemOneCloudflareConfig(t *testing.T, baseURL string) config.ServeOptions {
	t.Helper()
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
`, baseURL)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	return config.ServeOptions{BackendURL: baseURL}
}

func TestSystemOneMissingCredentialAnswersAsItselfAndSkipsTheBreaker(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	// The config load needs the key; it is gone by the time a request builds the
	// client, as when an operator rotates it out from under a running gateway.
	t.Setenv("CF_AI_API_KEY", "test-key")
	var dials atomic.Int32
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dials.Add(1)
		http.Error(w, "must not be dialled", http.StatusInternalServerError)
	}))
	t.Cleanup(cf.Close)
	opts := writeSystemOneCloudflareConfig(t, cf.URL)
	handler := newTestGateway(t, opts, newFakeRouter(t, opts))
	t.Setenv("CF_AI_API_KEY", "")

	for i := range int(upstream.ConsecutiveFailuresTrip) + 2 {
		req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(
			`{"model":"cf_local/typesafe/jev","state":"s","questions":{"q":{"type":"noul"}}}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "circuit breaker") {
			t.Fatalf("attempt %d: want the credential error (503), got %d %s", i, rec.Code, rec.Body.String())
		}
	}
	if dials.Load() != 0 {
		t.Fatalf("a client that cannot be built must not dial: %d", dials.Load())
	}
	gw, _ := gatewayFromHandler(handler)
	for _, s := range gw.systemOne.upstreams.Snapshot() {
		if s.Failures > 0 || s.State != "closed" {
			t.Fatalf("a missing credential must not count against the breaker: %+v", s)
		}
	}
}

// TestSystemOneRewritesTheModelAfterTheDial checks the Bifrost route still answers
// with the public model now that the rewrite runs after the breaker returns.
func TestSystemOneRewritesTheModelAfterTheDial(t *testing.T) {
	sr := &scriptedRouter{decision: func(context.Context, router.Call) ([]byte, error) {
		return []byte(`{"model":"inner","answers":{}}`), nil
	}}
	cfg := config.RouterConfig{
		SystemOne: config.CapabilityBackend{Enabled: true, Default: "typesafe"},
		Policy:    config.DefaultRouterPolicy(),
		Timeouts:  config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"typesafe": {
				ID: "typesafe", BaseURL: "http://127.0.0.1:9", Extension: config.ExtensionTypesafe,
				Capabilities: []config.Capability{config.CapSystemOne},
			},
		},
	}
	h := newScriptedGateway(t, sr, cfg)
	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(
		`{"model":"typesafe/jev","state":"s","questions":{"q":{"type":"noul"}}}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"model":"typesafe/jev"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestCatalogClientFailureSkipsTheBreaker(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")
	cfg := config.RouterConfig{
		Chat:     config.CapabilityBackend{Enabled: true, Default: "gem"},
		Policy:   config.DefaultRouterPolicy(),
		Timeouts: config.DefaultTimeouts(),
		Backends: map[string]config.BackendDef{
			"gem": {
				ID: "gem", BaseURL: "https://generativelanguage.googleapis.com/v1beta", Remote: true,
				Extension: config.ExtensionGemini, Capabilities: []config.Capability{config.CapChat},
			},
		},
	}
	var builds atomic.Int32
	failingGet := func(config.BackendDef) (*gemini.Client, error) {
		builds.Add(1)
		return nil, derrors.New(derrors.CodeNotReady, "gemini.Clients.Get", "GEMINI_API_KEY not set")
	}
	h := newScriptedGateway(t, &scriptedRouter{}, cfg, withGeminiClientGet(failingGet))
	gw, _ := gatewayFromHandler(h)
	b := cfg.Backends["gem"]
	for range int(upstream.ConsecutiveFailuresTrip) + 2 {
		gw.models.refreshBackend(t.Context(), "", b)
	}
	if builds.Load() < int32(upstream.ConsecutiveFailuresTrip)+2 {
		t.Fatalf("each refresh must try to build the client, got %d", builds.Load())
	}
	for _, s := range gw.models.upstreams.Snapshot() {
		if s.Name == "gem" {
			t.Fatalf("a client build failure must never reach the breaker: %+v", s)
		}
	}
}
