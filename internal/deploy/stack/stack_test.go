package stack

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSharedFilesMatchRepoRoot fails when the embedded copies drift from the files
// local serve uses at the repository root.
func TestSharedFilesMatchRepoRoot(t *testing.T) {
	for name, embedded := range map[string][]byte{"otelcol.config.yaml": otelcolConfig, "images.env": imagePins} {
		root, err := os.ReadFile(filepath.Join("..", "..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(root, embedded) {
			t.Fatalf("internal/deploy/stack/%s drifted from the repository root's; run go tool task sync-deploy-stack", name)
		}
	}
}

func TestWrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "polypus-work")
	compose, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	if compose != filepath.Join(dir, ComposeFileName) {
		t.Fatalf("compose path %q", compose)
	}
	for name := range files {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() == 0 {
			t.Fatalf("%s is empty", name)
		}
	}
	// A second deploy rewrites the same files.
	if _, err := Write(dir); err != nil {
		t.Fatal(err)
	}
}

// TestGatewayLoadsKeysFromEnvFile fails when the gateway service goes back to a
// hand-written list of backend keys, which drops any key the list forgets.
func TestGatewayLoadsKeysFromEnvFile(t *testing.T) {
	var doc struct {
		Services map[string]struct {
			EnvFile     []string          `yaml:"env_file"`
			Environment map[string]string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(composeFile, &doc); err != nil {
		t.Fatal(err)
	}
	gateway, ok := doc.Services["gateway"]
	if !ok {
		t.Fatal("compose file has no gateway service")
	}
	if !slices.Contains(gateway.EnvFile, ".env.gateway") {
		t.Fatalf("gateway env_file = %v, want .env.gateway", gateway.EnvFile)
	}
	for name := range gateway.Environment {
		if strings.HasSuffix(name, "_API_KEY") || name == "CF_ACCOUNT_ID" {
			t.Errorf("gateway environment lists %s; backend keys belong in .env.gateway", name)
		}
	}
}
