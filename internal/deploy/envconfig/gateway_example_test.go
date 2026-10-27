package envconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/deploy/gatewayconfig"
	"github.com/behaviorengineering/polypus/internal/deploy/routers"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// writeGatewayExample writes the embedded default gateway YAML to a temp file.
func writeGatewayExample(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(path, gatewayExample, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGatewayExampleLoads(t *testing.T) {
	path := writeGatewayExample(t)
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test-xxxxxxxxxxxxxxxx")
	cfg, err := config.LoadRouterConfig(config.ServeOptions{ConfigPath: path})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := router.NewRegistry(cfg); err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, ok := cfg.Backends["openrouter"]; !ok || len(cfg.Backends) != 1 {
		t.Fatalf("backends: want openrouter only, got %d", len(cfg.Backends))
	}
}

func TestGatewayExampleNeedsOnlyOpenRouterKey(t *testing.T) {
	names, err := gatewayconfig.SecretEnvNames(writeGatewayExample(t), secrets.DefaultNames)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TS_AUTHKEY", "OPENROUTER_API_KEY"}; !slices.Equal(names, want) {
		t.Fatalf("secrets %v want %v", names, want)
	}
}

func TestGatewayExampleHasTurnoRouter(t *testing.T) {
	has, err := routers.HasTopLevelRouters(writeGatewayExample(t))
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Fatal("the default should define routers (router/turno), so Switchyard runs")
	}
}
