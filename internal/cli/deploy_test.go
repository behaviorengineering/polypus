package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behaviorengineering/polypus/internal/deploy"
	"github.com/behaviorengineering/polypus/internal/deploy/envconfig"
)

// deployFixture lays out an isolated deploy state folder and an environment: the
// files a deploy plan reads, with no keyring or Docker involved. The user config dir is
// isolated so no test reads the developer's own environment.
func deployFixture(t *testing.T) (root, config, pins string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(envconfig.ConfigEnv, "")
	root = t.TempDir()
	t.Setenv(deploy.StateDirEnv, root)
	env := t.TempDir()
	pins = filepath.Join(env, "images.env")
	writeFiles(t, env, map[string]string{
		"config.yaml":  "identifier: work\ntailnet: corp.ts.net\ngateway: gateway.yaml\n",
		"gateway.yaml": "backends:\n  openrouter:\n    base_url: https://openrouter.ai/api/v1\n    auth:\n      bearer_env: OPENROUTER_API_KEY\n",
		"images.env": strings.Join([]string{
			"POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:0.3.0",
			"SWITCHYARD_IMAGE=ghcr.io/behaviorengineering/polypus-switchyard:0.3.0",
			"PHOENIX_IMAGE=arizephoenix/phoenix:version-5.8.0",
			"HYPERDX_IMAGE=clickhouse/clickstack-local:2.10.0",
			"TAILSCALE_IMAGE=tailscale/tailscale:v1.102.4",
			"OTELCOL_IMAGE=otel/opentelemetry-collector-contrib:0.120.0",
		}, "\n") + "\n",
	})
	return root, filepath.Join(env, "config.yaml"), pins
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeployBareShowsHelpExit2(t *testing.T) {
	code, out, _ := captureRun([]string{"deploy"})
	if code != 2 {
		t.Fatalf("exit %d want 2", code)
	}
	if !strings.Contains(out, "run") || !strings.Contains(out, "init") {
		t.Fatalf("deploy help missing subcommands: %q", out)
	}
}

func TestDeployRunDryRunWritesNothing(t *testing.T) {
	root, config, pins := deployFixture(t)
	code, out, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--config", config, "--images", pins})
	if code != 0 {
		t.Fatalf("exit %d want 0\nstdout=%s\nstderr=%s", code, out, errOut)
	}
	gateway := filepath.Join(filepath.Dir(config), "gateway.yaml")
	for _, want := range []string{"deploy: environment work (" + config + ")", "keyring service polypus-work", "health and smoke at https://polypus-work.corp.ts.net", "on tailnet corp.ts.net first", "deploy: gateway config " + gateway, "deploy: images " + pins, "OPENROUTER_API_KEY", "dry run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	for _, name := range []string{".env.deploy", ".env.images"} {
		if _, err := os.Stat(filepath.Join(root, "polypus-work", name)); !os.IsNotExist(err) {
			t.Fatalf("dry run wrote %s (stat err %v)", name, err)
		}
	}
}

func TestDeployRunFindsEnvironmentFromEnv(t *testing.T) {
	_, config, pins := deployFixture(t)
	t.Setenv(envconfig.ConfigEnv, config)
	code, out, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--images", pins})
	if code != 0 {
		t.Fatalf("exit %d want 0\nstderr=%s", code, errOut)
	}
	if !strings.Contains(out, "deploy: environment work") {
		t.Fatalf("stdout missing environment: %s", out)
	}
}

func TestDeployRunDevBuildNeedsImages(t *testing.T) {
	_, config, _ := deployFixture(t)
	code, _, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--config", config})
	if code != 1 {
		t.Fatalf("exit %d want 1", code)
	}
	if !strings.Contains(errOut, "--images") {
		t.Fatalf("stderr should name --images: %q", errOut)
	}
}

func TestDeployRunNeedsEnvironment(t *testing.T) {
	_, _, pins := deployFixture(t)
	code, _, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--images", pins})
	if code != 1 {
		t.Fatalf("exit %d want 1", code)
	}
	if !strings.Contains(errOut, "polypus deploy init") {
		t.Fatalf("stderr should point at init: %q", errOut)
	}
}

func TestDeployExportEnvDryRun(t *testing.T) {
	root, config, _ := deployFixture(t)
	code, out, errOut := captureRun([]string{"deploy", "export-env", "--dry-run", "--config", config})
	if code != 0 {
		t.Fatalf("exit %d want 0\nstderr=%s", code, errOut)
	}
	if !strings.Contains(out, "OPENROUTER_API_KEY") {
		t.Fatalf("stdout missing secret name: %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "polypus-work", ".env.deploy")); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote .env.deploy (stat err %v)", err)
	}
}

func TestDeployInit(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	dir := filepath.Join(xdg, "polypus", "deploy")

	code, out, errOut := captureRun([]string{"deploy", "init", "--dry-run"})
	if code != 0 || !strings.Contains(out, "would write") {
		t.Fatalf("dry run: exit %d stdout=%s stderr=%s", code, out, errOut)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s", dir)
	}

	if code, out, _ = captureRun([]string{"deploy", "init"}); code != 0 || !strings.Contains(out, "wrote") {
		t.Fatalf("first init: exit %d stdout=%s", code, out)
	}
	for _, name := range []string{"config.yaml", "gateway.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("init did not write %s: %v", name, err)
		}
	}
	if code, out, _ = captureRun([]string{"deploy", "init"}); code != 0 || strings.Contains(out, "wrote") {
		t.Fatalf("second init should keep both files: exit %d stdout=%s", code, out)
	}
}

func TestDeployRunPinsFollowObservability(t *testing.T) {
	_, config, _ := deployFixture(t)
	minimal := filepath.Join(filepath.Dir(config), "minimal.env")
	writeFiles(t, filepath.Dir(config), map[string]string{
		"minimal.env": "POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:0.3.0\nTAILSCALE_IMAGE=tailscale/tailscale:v1.102.4\n",
		"config.yaml": "identifier: work\ntailnet: corp.ts.net\nobservability: false\n",
	})
	code, out, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--config", config, "--images", minimal})
	if code != 0 {
		t.Fatalf("observability off with minimal pins: exit %d\nstderr=%s", code, errOut)
	}
	if !strings.Contains(out, "observability off") {
		t.Fatalf("stdout missing observability off:\n%s", out)
	}

	writeFiles(t, filepath.Dir(config), map[string]string{
		"config.yaml": "identifier: work\ntailnet: corp.ts.net\nobservability: true\n",
	})
	code, _, errOut = captureRun([]string{"deploy", "run", "--dry-run", "--config", config, "--images", minimal})
	if code != 1 || !strings.Contains(errOut, "PHOENIX_IMAGE") {
		t.Fatalf("observability on needs the Phoenix pin: exit %d stderr=%s", code, errOut)
	}
}

func TestDeployRunHealthURLOverrideSkipsTailnet(t *testing.T) {
	_, config, pins := deployFixture(t)
	code, out, errOut := captureRun([]string{"deploy", "run", "--dry-run", "--config", config, "--images", pins, "--health-url", "http://127.0.0.1:1320/"})
	if code != 0 {
		t.Fatalf("exit %d\nstderr=%s", code, errOut)
	}
	if !strings.Contains(out, "health and smoke at http://127.0.0.1:1320\n") || strings.Contains(out, "on tailnet") {
		t.Fatalf("an explicit --health-url should be used as is, with no tailnet check:\n%s", out)
	}
}
