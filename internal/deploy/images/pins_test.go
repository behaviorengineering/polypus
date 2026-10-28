package images

import (
	"strings"
	"testing"
)

func TestVersionPins(t *testing.T) {
	body := "# observability\nPHOENIX_IMAGE=arizephoenix/phoenix:version-5.8.0\nPOLYPUS_IMAGE=ghcr.io/x/polypus:0.0.1\nTAILSCALE_IMAGE=tailscale/tailscale:v1.102.4\n"
	got, err := VersionPins([]byte(body), "v0.4.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:0.4.0\n",
		"SWITCHYARD_IMAGE=ghcr.io/behaviorengineering/polypus-switchyard:0.4.0\n",
		"PHOENIX_IMAGE=arizephoenix/phoenix:version-5.8.0\n",
		"TAILSCALE_IMAGE=tailscale/tailscale:v1.102.4\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("pins missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ghcr.io/x/polypus") {
		t.Fatalf("repo POLYPUS_IMAGE must be replaced by the release image:\n%s", got)
	}
}

func TestVersionPinsDevBuild(t *testing.T) {
	if _, err := VersionPins(nil, "dev"); err == nil {
		t.Fatal("expected a dev build to need a pinned images file")
	}
}
