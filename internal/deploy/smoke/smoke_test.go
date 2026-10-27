package smoke

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSystemOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		case "/v1/systemone":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"jev-test","answers":{"is_urgent":{"type":"noul","noul":0.9}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, Options{BaseURL: srv.URL, WithSystemOne: true}); err != nil {
		t.Fatal(err)
	}
}

func TestRunSystemOneFromGateway(t *testing.T) {
	dir := t.TempDir()
	gw := filepath.Join(dir, "gw.yaml")
	if err := os.WriteFile(gw, []byte(`backends:
  cf_local:
    extension: cloudflare
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [systemone]
  openrouter:
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [systemone]
    models:
      systemone_allow:
        - perplexity/pplx-decider-v1.2
        - perplexity/pplx-decider-flash-v1.2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, ".env.deploy")
	if err := os.WriteFile(env, []byte("CF_AI_API_KEY=x\nCF_ACCOUNT_ID=1\nOPENROUTER_API_KEY=or\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		case "/v1/systemone":
			body, _ := readBody(r)
			var raw map[string]any
			_ = json.Unmarshal([]byte(body), &raw)
			if m, ok := raw["model"].(string); ok {
				models = append(models, m)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"model":"decider","answers":{"is_urgent":{"type":"noul","noul":0.9}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, Options{
		BaseURL:       srv.URL,
		WithSystemOne: true,
		GatewayConfig: gw,
		EnvDeploy:     env,
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"cf_local/typesafe/jev":                   false,
		"openrouter/perplexity/pplx-decider-v1.2": false,
	}
	for _, m := range models {
		if _, ok := want[m]; ok {
			want[m] = true
		}
	}
	for model, seen := range want {
		if !seen {
			t.Fatalf("missing systemone probe %s in %v", model, models)
		}
	}
}

func TestRunSystemOneRequiresOpenRouterKey(t *testing.T) {
	dir := t.TempDir()
	gw := filepath.Join(dir, "gw.yaml")
	if err := os.WriteFile(gw, []byte(`backends:
  cf_local:
    extension: cloudflare
    auth:
      bearer_env: CF_AI_API_KEY
    capabilities: [systemone]
  openrouter:
    auth:
      bearer_env: OPENROUTER_API_KEY
    capabilities: [systemone]
    models:
      systemone_allow:
        - perplexity/pplx-decider-v1.2
`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, ".env.deploy")
	if err := os.WriteFile(env, []byte("CF_AI_API_KEY=x\nCF_ACCOUNT_ID=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var models []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		case "/v1/systemone":
			body, _ := readBody(r)
			var raw map[string]any
			_ = json.Unmarshal([]byte(body), &raw)
			if m, ok := raw["model"].(string); ok {
				models = append(models, m)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"answers":{"is_urgent":{"type":"noul","noul":0.9}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := Run(ctx, Options{
		BaseURL:       srv.URL,
		WithSystemOne: true,
		GatewayConfig: gw,
		EnvDeploy:     env,
	})
	if err == nil || !strings.Contains(err.Error(), "openrouter/perplexity/pplx-decider-v1.2") {
		t.Fatalf("expected required openrouter decider error, got %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("decider must not skip; got probes %v", models)
	}
}

func TestRunChatOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Run(ctx, Options{BaseURL: srv.URL, WithRouter: false}); err != nil {
		t.Fatal(err)
	}
}

func TestRunRouterRequiresSelectedInAllowList(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			calls++
			body, _ := readBody(r)
			w.Header().Set("Content-Type", "application/json")
			if strings.Contains(body, "router/investigator") {
				w.Header().Set("x-model-router-selected-model", defaultCapable)
				_, _ = w.Write([]byte(`{"model":"` + defaultCapable + `","choices":[{"message":{"content":"ok"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, Options{BaseURL: srv.URL, WithRouter: true}); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("expected chat + router calls, got %d", calls)
	}
}

func TestRunRouterFailsWrongLeaf(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-model-router-selected-model", "cf_local/@cf/wrong")
			_, _ = w.Write([]byte(`{"model":"cf_local/@cf/wrong","choices":[{"message":{"content":"ok"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := Run(ctx, Options{BaseURL: srv.URL, WithRouter: true})
	if err == nil || !strings.Contains(err.Error(), "not in allow-list") {
		t.Fatalf("expected allow-list error, got %v", err)
	}
}

func TestRunNilContextFails(t *testing.T) {
	var ctx context.Context //nolint:staticcheck // deliberate nil tests fail-closed guard
	if err := Run(ctx, Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func readBody(r *http.Request) (string, error) {
	var buf strings.Builder
	if r.Body == nil {
		return "", nil
	}
	dec := json.NewDecoder(r.Body)
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return "", err
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	buf.Write(b)
	return buf.String(), nil
}
