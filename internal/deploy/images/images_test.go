package images

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestVerifyOK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "images.env")
	content := `POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:1.0.0
SWITCHYARD_IMAGE=ghcr.io/behaviorengineering/polypus-switchyard:1.0.0
PHOENIX_IMAGE=arizephoenix/phoenix:version-4.5.0
HYPERDX_IMAGE=clickhouse/clickstack-local:2.10.0
TAILSCALE_IMAGE=tailscale/tailscale:v1.102.4
OTELCOL_IMAGE=otel/opentelemetry-collector-contrib:0.120.0
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, Services{Routers: true, Observability: true}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "images.env")
	content := `POLYPUS_IMAGE=ghcr.io/x/y:latest
SWITCHYARD_IMAGE=ghcr.io/x/s:1.0.0
PHOENIX_IMAGE=p:1
HYPERDX_IMAGE=h:1
TAILSCALE_IMAGE=tailscale/tailscale:v1.0.0
OTELCOL_IMAGE=otel/opentelemetry-collector-contrib:0.120.0
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(path, Services{Routers: true, Observability: true}); err == nil {
		t.Fatal("expected error for :latest")
	}
}

func TestVerifyPinsNeedsATagOrDigest(t *testing.T) {
	base := "POLYPUS_IMAGE=%s\nTAILSCALE_IMAGE=tailscale/tailscale:v1.0.0\n"
	for ref, ok := range map[string]bool{
		"ghcr.io/x/y:1.0.0":        true,
		"ghcr.io/x/y@sha256:abc":   true,
		"localhost:5000/x/y:1.0.0": true,
		"ghcr.io/x/y":              false,
		"localhost:5000/x/y":       false,
		"ghcr.io/x/y:latest":       false,
		"ghcr.io/x/y:":             false,
	} {
		err := VerifyPins(fmt.Sprintf(base, ref), "images.env", Services{})
		if ok && err != nil {
			t.Errorf("%s: %v, want accepted", ref, err)
		}
		if !ok && derrors.CodeOf(err) != derrors.CodeInvalid {
			t.Errorf("%s: err = %v, want refused as invalid", ref, err)
		}
	}
}
