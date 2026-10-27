package deploy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/internal/deploy/images"
)

func TestPostComposeUpReloadPrunesBeforeRecreate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var order []string
	opts := Options{
		PruneFunc: func(ctx context.Context, gatewayPath string) error {
			order = append(order, "prune")
			return nil
		},
	}
	err := postComposeUpReload(ctx, opts, "/gateway.yaml", func(ctx context.Context, services ...string) error {
		order = append(order, "recreate")
		return nil
	}, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prune", "recreate"}
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
	root := t.TempDir()
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
	opts := DefaultOptions(root)
	opts.Name = "polypus-work"
	opts.GatewayConfig = gateway
	opts.PinsFile = pins
	opts.HealthURL = "https://polypus-work.corp.ts.net"
	opts.Tailnet = "corp.ts.net"
	opts.TailnetStatus = func(context.Context) ([]byte, error) {
		return []byte(`{"BackendState":"Running","CurrentTailnet":{"MagicDNSSuffix":"home.ts.net","MagicDNSEnabled":true}}`), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, opts)
	if err == nil || !strings.Contains(err.Error(), "home.ts.net") {
		t.Fatalf("err = %v, want the wrong tailnet named", err)
	}
	for _, p := range []string{opts.EnvDeploy, opts.StateDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("deploy wrote %s before the tailnet check (stat err %v)", p, err)
		}
	}
}
