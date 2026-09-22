package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
)

func TestSystemOnePassthrough(t *testing.T) {
	t.Setenv("POLYPUS_SWITCHYARD", "0")

	var gotBody []byte
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"decider-dev","answers":{"is_urgent":{"type":"noul","noul":0.9}},"usage":{"input_tokens":10,"output_tokens":5}}`))
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
    base_url: %s
    capabilities: [systemone, tts, stt, voices]
    models:
      allow:
        - typesafe/jev
`, upstream.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)

	opts := config.ServeOptions{BackendURL: upstream.URL}
	fake := newFakeRouter(t, opts)
	handler := newTestGateway(t, opts, fake)

	req := httptest.NewRequest(http.MethodPost, "/v1/systemone", strings.NewReader(`{
		"model": "decider/typesafe/jev",
		"state": "Help! payouts failing for 3 days.",
		"questions": {
			"is_urgent": {
				"type": "noul",
				"instructions": "Does this convey urgency?"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/v1/systemone" {
		t.Fatalf("upstream path %q", gotPath)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(gotBody, &forwarded); err != nil {
		t.Fatal(err)
	}
	if forwarded["model"] != "typesafe/jev" {
		t.Fatalf("forwarded model %v", forwarded["model"])
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["model"] != "decider/typesafe/jev" {
		t.Fatalf("response model %v", resp["model"])
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
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
    base_url: %s
    capabilities: [systemone, tts, stt, voices]
    models:
      allow:
        - typesafe/jev
`, upstream.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
    base_url: %s
    capabilities: [systemone, tts, stt, voices]
`, upstream.URL)
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
