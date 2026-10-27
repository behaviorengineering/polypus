package deploy

import (
	"context"
	"slices"
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
