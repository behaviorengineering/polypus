package smoke

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

// routerGateway writes a gateway YAML with a composed router (investigator) and a
// passthrough router (turno).
func routerGateway(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	body := `routers:
  investigator:
    capability: chat
    route:
      type: stage_router
      capable: cf_local/big
      efficient: cf_local/small
  turno:
    capability: chat
    route:
      type: passthrough
      target: openrouter/anthropic/claude-sonnet-5.5
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// routerServer answers chat as a gateway would: investigator names its leaf in the
// routing header, turno reports only the upstream model in the body.
func routerServer(t *testing.T, investigatorLeaf string, models *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			body, _ := readBody(r)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case strings.Contains(body, "router/investigator"):
				*models = append(*models, "router/investigator")
				w.Header().Set("x-model-router-selected-model", investigatorLeaf)
				_, _ = w.Write([]byte(`{"model":"` + investigatorLeaf + `","choices":[{"message":{"content":"ok"}}]}`))
			case strings.Contains(body, "router/turno"):
				*models = append(*models, "router/turno")
				_, _ = w.Write([]byte(`{"model":"anthropic/claude-sonnet-5.5","choices":[{"message":{"content":"ok"}}]}`))
			default:
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunRoutersFromGateway(t *testing.T) {
	var models []string
	srv := routerServer(t, "cf_local/big", &models)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := Run(ctx, Options{BaseURL: srv.URL, WithRouter: true, GatewayConfig: routerGateway(t)}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"router/investigator", "router/turno"}; !slices.Equal(models, want) {
		t.Fatalf("routers smoked %v want %v", models, want)
	}
}

func TestRunRouterFailsWrongLeaf(t *testing.T) {
	var models []string
	srv := routerServer(t, "cf_local/wrong", &models)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := Run(ctx, Options{BaseURL: srv.URL, WithRouter: true, GatewayConfig: routerGateway(t)})
	if err == nil || !strings.Contains(err.Error(), "not in allow-list") {
		t.Fatalf("expected allow-list error, got %v", err)
	}
}

func TestRunRouterNeedsRouters(t *testing.T) {
	var models []string
	srv := routerServer(t, "cf_local/big", &models)
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, []byte("backends: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := Run(ctx, Options{BaseURL: srv.URL, WithRouter: true, GatewayConfig: path})
	if err == nil || !strings.Contains(err.Error(), "no chat routers") {
		t.Fatalf("expected no-routers error, got %v", err)
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
