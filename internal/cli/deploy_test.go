package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deployFixture lays out a minimal Polypus checkout and environment: the files a
// deploy plan reads, with no keyring or Docker involved.
func deployFixture(t *testing.T) (root, gateway, pins string) {
	t.Helper()
	root = t.TempDir()
	files := map[string]string{
		"go.mod":                    "module example.com/polypus\n",
		"docker-compose.deploy.yml": "services: {}\n",
		"otelcol.config.yaml":       "receivers: {}\n",
		"images.env":                "PHOENIX_IMAGE=arizephoenix/phoenix:version-5.8.0\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := t.TempDir()
	gateway = filepath.Join(env, "gateway.yaml")
	gw := "backends:\n  openrouter:\n    base_url: https://openrouter.ai/api/v1\n    auth:\n      bearer_env: OPENROUTER_API_KEY\n"
	if err := os.WriteFile(gateway, []byte(gw), 0o600); err != nil {
		t.Fatal(err)
	}
	pins = filepath.Join(env, "images.env")
	body := strings.Join([]string{
		"POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:0.3.0",
		"SWITCHYARD_IMAGE=ghcr.io/behaviorengineering/polypus-switchyard:0.3.0",
		"PHOENIX_IMAGE=arizephoenix/phoenix:version-5.8.0",
		"HYPERDX_IMAGE=clickhouse/clickstack-local:2.10.0",
		"TAILSCALE_IMAGE=tailscale/tailscale:v1.102.4",
		"OTELCOL_IMAGE=otel/opentelemetry-collector-contrib:0.120.0",
	}, "\n") + "\n"
	if err := os.WriteFile(pins, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, gateway, pins
}

func TestDeployBareShowsHelpExit2(t *testing.T) {
	code, out, _ := captureRun([]string{"deploy"})
	if code != 2 {
		t.Fatalf("exit %d want 2", code)
	}
	if !strings.Contains(out, "run") || !strings.Contains(out, "export-env") {
		t.Fatalf("deploy help missing subcommands: %q", out)
	}
}

func TestDeployRunDryRunWritesNothing(t *testing.T) {
	root, gateway, pins := deployFixture(t)
	code, out, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--root", root, "--gateway-config", gateway, "--images", pins})
	if code != 0 {
		t.Fatalf("exit %d want 0\nstdout=%s\nstderr=%s", code, out, errOut)
	}
	for _, want := range []string{"deploy: gateway config " + gateway, "deploy: images " + pins, "OPENROUTER_API_KEY", "dry run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	for _, name := range []string{".env.deploy", ".env.images"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("dry run wrote %s (stat err %v)", name, err)
		}
	}
}

func TestDeployRunDevBuildNeedsImages(t *testing.T) {
	root, gateway, _ := deployFixture(t)
	code, _, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--root", root, "--gateway-config", gateway})
	if code != 1 {
		t.Fatalf("exit %d want 1", code)
	}
	if !strings.Contains(errOut, "--images") {
		t.Fatalf("stderr should name --images: %q", errOut)
	}
}

func TestDeployRunNeedsGatewayConfig(t *testing.T) {
	root, _, pins := deployFixture(t)
	code, _, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--root", root, "--images", pins})
	if code != 1 {
		t.Fatalf("exit %d want 1", code)
	}
	if !strings.Contains(errOut, "--gateway-config") {
		t.Fatalf("stderr should name --gateway-config: %q", errOut)
	}
}

func TestDeployExportEnvDryRun(t *testing.T) {
	root, gateway, _ := deployFixture(t)
	code, out, errOut := captureRun([]string{"deploy", "export-env", "--dry-run", "--root", root, "--gateway-config", gateway})
	if code != 0 {
		t.Fatalf("exit %d want 0\nstderr=%s", code, errOut)
	}
	if !strings.Contains(out, "OPENROUTER_API_KEY") {
		t.Fatalf("stdout missing secret name: %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, ".env.deploy")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote .env.deploy (stat err %v)", err)
	}
}
