package deploy

import (
	"context"
	"slices"
	"testing"
	"time"
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

func TestBindMountReloadServicesIncludesTailscale(t *testing.T) {
	want := []string{"gateway", "otelcol", "tailscale"}
	if !slices.Equal(bindMountReloadServices, want) {
		t.Fatalf("bindMountReloadServices = %q, want %q", bindMountReloadServices, want)
	}
}
