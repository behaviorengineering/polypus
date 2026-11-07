package deploy

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/compose"
	"github.com/behaviorengineering/polypus/internal/deploy/images"
	"github.com/behaviorengineering/polypus/internal/deploy/progress"
)

func TestPostComposeUpReloadPrunesBeforeRecreate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var order []string
	opts := Options{
		PruneFunc: func(ctx context.Context, gatewayPath string) error {
			order = append(order, "prune "+gatewayPath)
			return nil
		},
	}
	cc := compose.Client{ComposeFile: "c.yml", Run: func(_ context.Context, _ string, args, _ []string) error {
		order = append(order, "docker "+strings.Join(args[len(args)-2:], " "))
		return nil
	}}
	err := postComposeUpReload(ctx, opts, progress.New(io.Discard), cc, plan{gatewayPath: "/gateway.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prune /gateway.yaml", "docker gateway tailscale"}
	if !slices.Equal(order, want) {
		t.Fatalf("order %v want %v", order, want)
	}
}

func TestReloadServicesFollowObservability(t *testing.T) {
	if got, want := reloadServices(images.Services{Observability: true}), []string{"gateway", "otelcol", "tailscale"}; !slices.Equal(got, want) {
		t.Fatalf("observability on: %q want %q", got, want)
	}
	if got, want := reloadServices(images.Services{}), []string{"gateway", "tailscale"}; !slices.Equal(got, want) {
		t.Fatalf("observability off: %q want %q", got, want)
	}
}

func TestComposeProfiles(t *testing.T) {
	for _, tc := range []struct {
		svc  images.Services
		want string
	}{
		{images.Services{}, ""},
		{images.Services{Routers: true}, "routers"},
		{images.Services{Observability: true}, "observability"},
		{images.Services{Routers: true, Observability: true}, "routers,observability"},
	} {
		if got := composeProfiles(tc.svc); got != tc.want {
			t.Fatalf("%+v: %q want %q", tc.svc, got, tc.want)
		}
	}
}

func TestRunStopsOffTailnetBeforeWriting(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	env := t.TempDir()
	gateway := filepath.Join(env, "gateway.yaml")
	pins := filepath.Join(env, "images.env")
	files := map[string]string{
		gateway: "backends:\n  openrouter:\n    auth:\n      bearer_env: OPENROUTER_API_KEY\n",
		pins:    "POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:0.3.0\nTAILSCALE_IMAGE=tailscale/tailscale:v1.102.4\n",
	}
	for p, body := range files {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	opts := DefaultOptions(state)
	opts.Name = "polypus-work"
	opts.GatewayConfig = gateway
	opts.PinsFile = pins
	opts.HealthURL = "https://polypus-work.corp.ts.net"
	opts.Tailnet = "corp.ts.net"
	var out strings.Builder
	opts.Out = &out
	opts.TailnetStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"BackendState":"Running","CurrentTailnet":{"MagicDNSSuffix":"home.ts.net","MagicDNSEnabled":true}}`), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, opts)
	if err == nil || !strings.Contains(err.Error(), "home.ts.net") {
		t.Fatalf("err = %v, want the wrong tailnet named", err)
	}
	if !strings.Contains(out.String(), "deploy: checks this machine is on tailnet corp.ts.net first") {
		t.Fatalf("plan not written to Options.Out: %q", out.String())
	}
	for _, p := range []string{opts.EnvDeployPath(), opts.StateDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("deploy wrote %s before the tailnet check (stat err %v)", p, err)
		}
	}
}

func TestDefaultStateDirOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(StateDirEnv, dir)
	got, err := DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("state dir %q want %q", got, dir)
	}
	opts := DefaultOptions(got)
	opts.Name = "polypus-work"
	if want := filepath.Join(dir, "polypus-work", ".env.deploy"); opts.EnvDeployPath() != want {
		t.Fatalf("env deploy %q want %q", opts.EnvDeployPath(), want)
	}
}

func TestComposeEnvLeavesProcessEnvironmentAlone(t *testing.T) {
	t.Setenv("COMPOSE_PROFILES", "operator")
	env := composeEnv(Options{Name: "polypus-work"}, plan{gatewayPath: "/g.yaml", services: images.Services{Routers: true}}, "/serve")
	if !slices.Contains(env, "COMPOSE_PROFILES=routers") || !slices.Contains(env, "POLYPUS_SWITCHYARD=1") {
		t.Fatalf("env %v", env)
	}
	if os.Getenv("COMPOSE_PROFILES") != "operator" {
		t.Fatal("composeEnv must not change the process environment")
	}
	off := composeEnv(Options{}, plan{}, "/serve")
	if !slices.Contains(off, "COMPOSE_PROFILES=") {
		t.Fatalf("no profiles must still override the operator's: %v", off)
	}
}
