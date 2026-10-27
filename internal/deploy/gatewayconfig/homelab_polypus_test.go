package gatewayconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/config"
	"github.com/behaviorengineering/polypus/internal/deploy/secrets"
	"github.com/behaviorengineering/polypus/internal/gateway/router"
)

// homelabFixture is a copy of the homelab environment's gateway YAML, so these tests
// run against a real deploy config without the environment's repository.
const homelabFixture = "testdata/gateway.yaml"

const (
	dummyCFAccountID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	dummySecretVal   = "xxxxxxxxxxxxxxxxxxxx"
)

func TestHomelabGatewayLoadRouterConfig(t *testing.T) {
	setDummySecretEnv(t, homelabFixture)
	if err := loadGatewayConfig(homelabFixture); err != nil {
		t.Fatal(err)
	}
}

func TestHomelabGatewayLoadRouterConfigMissingBaseURL(t *testing.T) {
	data, err := os.ReadFile(homelabFixture)
	if err != nil {
		t.Fatal(err)
	}
	// Gemini backends may omit base_url at load (extension default); cf_local must have one.
	stripped := stripBackendBaseURL(data, "cf_local")
	tmp := filepath.Join(t.TempDir(), "gateway.yaml")
	if err := os.WriteFile(tmp, stripped, 0o600); err != nil {
		t.Fatal(err)
	}
	setDummySecretEnv(t, tmp)
	if err := loadGatewayConfig(tmp); err == nil {
		t.Fatal("expected the gateway config without cf_local base_url to fail to load")
	}
}

// loadGatewayConfig loads path the way the gateway does at start: the router config,
// then the backend registry built from it.
func loadGatewayConfig(path string) error {
	cfg, err := config.LoadRouterConfig(config.ServeOptions{ConfigPath: path})
	if err != nil {
		return err
	}
	_, err = router.NewRegistry(cfg)
	return err
}

func setDummySecretEnv(t *testing.T, gatewayPath string) {
	names, err := SecretEnvNames(gatewayPath, secrets.DefaultNames)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		val := dummySecretVal
		if name == "CF_ACCOUNT_ID" {
			val = dummyCFAccountID
		}
		t.Setenv(name, val)
	}
}

func stripBackendBaseURL(yaml []byte, backendID string) []byte {
	marker := "  " + backendID + ":"
	lines := strings.Split(strings.ReplaceAll(string(yaml), "\r\n", "\n"), "\n")
	inBlock := false
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if line == marker {
			inBlock = true
			out = append(out, line)
			continue
		}
		if inBlock {
			if strings.HasPrefix(line, "    base_url:") {
				continue
			}
			if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") {
				inBlock = false
			}
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}
