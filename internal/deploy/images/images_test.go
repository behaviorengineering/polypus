package images

import (
	"os"
	"path/filepath"
	"testing"
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
