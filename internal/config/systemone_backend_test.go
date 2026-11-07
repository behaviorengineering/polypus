package config

import (
	"strings"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// loadSystemOneBackend loads a config whose systemone default is the backend
// "decider", declared by entry (its YAML lines under backends.decider).
func loadSystemOneBackend(t *testing.T, entry string) (RouterConfig, error) {
	t.Helper()
	path := t.TempDir() + "/config.yaml"
	content := `
systemone_backend:
  enabled: true
  default: decider
backends:
  decider:
` + entry
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POLYPUS_CONFIG", path)
	return LoadRouterConfig(ServeOptions{})
}

func TestSystemOneBackendNeedsADecisionRoute(t *testing.T) {
	_, err := loadSystemOneBackend(t, `    base_url: http://127.0.0.1:1234
    capabilities: [chat, systemone]
`)
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("systemone on a plain OpenAI backend must fail load as CodeInvalid, got %v", err)
	}
	if !strings.Contains(err.Error(), "backends.decider") {
		t.Fatalf("the error must name the backend: %v", err)
	}
}

func TestSystemOneTypesafeBackendLoads(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "typesafe-secret-key-for-tests")
	cfg, err := loadSystemOneBackend(t, `    remote: true
    extension: typesafe
    auth:
      bearer_env: TYPESAFE_API_KEY
    capabilities: [systemone]
`)
	if err != nil {
		t.Fatal(err)
	}
	b := cfg.Backends["decider"]
	if !b.IsTypesafeExtension() || !b.hasSystemOneRoute() {
		t.Fatalf("backend: %+v", b)
	}
	if b.BaseURL != "" {
		t.Fatalf("typesafe base_url should stay empty for Bifrost's default host, got %q", b.BaseURL)
	}
}

func TestHasSystemOneRoute(t *testing.T) {
	cases := []struct {
		b    BackendDef
		want bool
	}{
		{BackendDef{ID: "cf_local", Extension: ExtensionCloudflare}, true},
		{BackendDef{ID: "openrouter"}, true},
		{BackendDef{ID: "jev", Extension: ExtensionTypesafe}, true},
		{BackendDef{ID: "or_mirror", BaseURL: "https://openrouter.ai/api/v1"}, false},
		{BackendDef{ID: "lm_studio"}, false},
	}
	for _, tc := range cases {
		if got := tc.b.hasSystemOneRoute(); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.b.ID, got, tc.want)
		}
	}
}
