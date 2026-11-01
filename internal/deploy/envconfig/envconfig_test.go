package envconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate points the user config dir and the override env at nothing, so a test
// never reads the developer's own environment.
func isolate(t *testing.T) string {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv(ConfigEnv, "")
	return xdg
}

func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validConfig = "identifier: work\ntailnet: corp.ts.net\n"

func TestResolveOrder(t *testing.T) {
	xdg := isolate(t)
	user := writeConfig(t, filepath.Join(xdg, "polypus", "deploy"), validConfig)
	envPath := writeConfig(t, t.TempDir(), validConfig)
	flagPath := writeConfig(t, t.TempDir(), validConfig)

	got, err := Resolve("")
	if err != nil || got != user {
		t.Fatalf("user config: got %q, %v want %q", got, err, user)
	}
	t.Setenv(ConfigEnv, envPath)
	if got, err = Resolve(""); err != nil || got != envPath {
		t.Fatalf("env override: got %q, %v want %q", got, err, envPath)
	}
	if got, err = Resolve(flagPath); err != nil || got != flagPath {
		t.Fatalf("flag override: got %q, %v want %q", got, err, flagPath)
	}
}

func TestResolveNotFound(t *testing.T) {
	isolate(t)
	_, err := Resolve("")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "polypus deploy init") {
		t.Fatalf("error should point at init: %v", err)
	}
}

func TestResolveMissingOverride(t *testing.T) {
	isolate(t)
	if _, err := Resolve(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected an error for a missing --config file")
	}
}

func TestLoadRelativePaths(t *testing.T) {
	dir := t.TempDir()
	p := writeConfig(t, dir, validConfig+"observability: true\ngateway: gw/gateway.yaml\nimages: images.env\n")
	env, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if env.Identifier != "work" || env.Tailnet != "corp.ts.net" || !env.Observability {
		t.Fatalf("fields: %+v", env)
	}
	if want := filepath.Join(dir, "gw", "gateway.yaml"); env.Gateway != want {
		t.Fatalf("gateway %q want %q", env.Gateway, want)
	}
	if want := filepath.Join(dir, "images.env"); env.Images != want {
		t.Fatalf("images %q want %q", env.Images, want)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	env, err := Load(writeConfig(t, dir, validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "gateway.yaml"); env.Gateway != want {
		t.Fatalf("gateway %q want %q", env.Gateway, want)
	}
	if env.Images != "" || !env.Observability {
		t.Fatalf("defaults: %+v", env)
	}
}

func TestLoadObservabilityOff(t *testing.T) {
	env, err := Load(writeConfig(t, t.TempDir(), validConfig+"observability: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if env.Observability {
		t.Fatalf("observability: false should turn it off: %+v", env)
	}
}

func TestLoadAbsoluteGateway(t *testing.T) {
	gw := filepath.Join(t.TempDir(), "gateway.yaml")
	env, err := Load(writeConfig(t, t.TempDir(), validConfig+"gateway: "+gw+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if env.Gateway != gw {
		t.Fatalf("gateway %q want %q", env.Gateway, gw)
	}
}

func TestLoadRejects(t *testing.T) {
	for name, body := range map[string]string{
		"missing identifier": "tailnet: corp.ts.net\n",
		"bad identifier":     "identifier: Work_1\ntailnet: corp.ts.net\n",
		"trailing hyphen":    "identifier: work-\ntailnet: corp.ts.net\n",
		"missing tailnet":    "identifier: work\n",
		"example tailnet":    "identifier: work\ntailnet: example.ts.net\n",
		"unknown field":      validConfig + "tailscale_host: x\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, t.TempDir(), body)); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}

func TestInitTwiceKeepsFiles(t *testing.T) {
	isolate(t)
	first, err := Init(false)
	if err != nil {
		t.Fatal(err)
	}
	if !first.WroteConfig || !first.WroteGateway {
		t.Fatalf("first init should write both files: %+v", first)
	}
	for _, p := range []string{first.ConfigPath, first.GatewayPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("%s mode %o want 600", p, mode)
		}
	}
	edited := []byte("identifier: mine\ntailnet: corp.ts.net\n")
	if err := os.WriteFile(first.ConfigPath, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := Init(false)
	if err != nil {
		t.Fatal(err)
	}
	if second.WroteConfig || second.WroteGateway {
		t.Fatalf("second init should keep both files: %+v", second)
	}
	got, err := os.ReadFile(first.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(edited) {
		t.Fatalf("second init changed config.yaml:\n%s", got)
	}
	if _, err := os.Stat(first.ExamplePath); err != nil {
		t.Fatalf("example not refreshed: %v", err)
	}
}

func TestInitForceRewrites(t *testing.T) {
	isolate(t)
	if _, err := Init(false); err != nil {
		t.Fatal(err)
	}
	res, err := Init(true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.WroteConfig || !res.WroteGateway {
		t.Fatalf("force should rewrite both files: %+v", res)
	}
}

func TestPlanInitWritesNothing(t *testing.T) {
	isolate(t)
	res, err := PlanInit(false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.WroteConfig || !res.WroteGateway {
		t.Fatalf("plan should report both files: %+v", res)
	}
	if _, err := os.Stat(res.Dir); !os.IsNotExist(err) {
		t.Fatalf("plan created %s (stat err %v)", res.Dir, err)
	}
}

func TestExampleIsNotDeployable(t *testing.T) {
	isolate(t)
	res, err := Init(false)
	if err != nil {
		t.Fatal(err)
	}
	// The written example keeps the placeholder tailnet, so a deploy cannot run
	// until the operator sets their own.
	if _, err := Load(res.ConfigPath); err == nil || !strings.Contains(err.Error(), "tailnet") {
		t.Fatalf("err = %v, want the placeholder tailnet rejected", err)
	}
}

func TestEnvironmentName(t *testing.T) {
	env, err := Load(writeConfig(t, t.TempDir(), validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.Name(); got != "polypus-work" {
		t.Fatalf("name %q want polypus-work", got)
	}
}

func TestEnvironmentGatewayURL(t *testing.T) {
	env, err := Load(writeConfig(t, t.TempDir(), validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if got := env.GatewayURL(); got != "https://polypus-work.corp.ts.net" {
		t.Fatalf("gateway URL %q", got)
	}
}
