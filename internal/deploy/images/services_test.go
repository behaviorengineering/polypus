package images

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const minimalPins = "POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:1.0.0\nTAILSCALE_IMAGE=tailscale/tailscale:v1.102.4\n"

func TestVerifyPinsMinimal(t *testing.T) {
	if err := VerifyPins(minimalPins, "pins", Services{}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPins(minimalPins, "pins", Services{Routers: true}); err == nil {
		t.Fatal("routers need SWITCHYARD_IMAGE")
	}
	if err := VerifyPins(minimalPins, "pins", Services{Observability: true}); err == nil {
		t.Fatal("observability needs the Phoenix, HyperDX and collector pins")
	}
}

func TestVerifyPinsRejectsLatestOnUnneededPin(t *testing.T) {
	pins := minimalPins + "PHOENIX_IMAGE=arizephoenix/phoenix:latest\n"
	if err := VerifyPins(pins, "pins", Services{}); err == nil {
		t.Fatal("expected :latest rejected even on a pin the services do not need")
	}
}

func TestEnsurePresentProbesSwitchyardOnlyWithRouters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "images.env")
	pins := minimalPins + "SWITCHYARD_IMAGE=ghcr.io/behaviorengineering/polypus-switchyard:1.0.0\n"
	if err := os.WriteFile(path, []byte(pins), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		svc  Services
		want []string
	}{
		{Services{}, []string{"ghcr.io/behaviorengineering/polypus:1.0.0"}},
		{Services{Routers: true}, []string{"ghcr.io/behaviorengineering/polypus:1.0.0", "ghcr.io/behaviorengineering/polypus-switchyard:1.0.0"}},
	} {
		var probed []string
		err := EnsurePresent(context.Background(), path, EnsureOptions{
			Services: tc.svc,
			Probe: func(_ context.Context, ref, _ string) error {
				probed = append(probed, ref)
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(probed, tc.want) {
			t.Fatalf("services %+v probed %v want %v", tc.svc, probed, tc.want)
		}
	}
}
