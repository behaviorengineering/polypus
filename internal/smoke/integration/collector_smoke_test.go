//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/behaviorengineering/polypus/pkg/smoke/otelcol"
)

func TestSmokeOtelFanout(t *testing.T) {
	if os.Getenv("POLYPUS_SMOKE_OTEL") != "1" {
		t.Skip("opt-in: POLYPUS_SMOKE_OTEL=1 with Docker for phoenix, hyperdx, otelcol")
	}
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := dockerComposeUp(ctx, root); err != nil {
		t.Fatalf("obs stack: %v", err)
	}
	if err := waitOtelcolReady(ctx); err != nil {
		t.Fatalf("otelcol: %v", err)
	}

	smokeCtx, smokeCancel := context.WithTimeout(ctx, otelcol.DefaultCollectorSmokeTimeout)
	defer smokeCancel()
	if err := otelcol.RunCollector(smokeCtx, otelcol.CollectorOptions{
		OTLPEndpoint: "http://127.0.0.1:4317",
	}); err != nil {
		t.Fatal(err)
	}
}

func dockerComposeUp(ctx context.Context, root string) error {
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", filepath.Join(root, "docker-compose.yml"),
		"up", "-d", "phoenix", "hyperdx", "otelcol")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

func waitOtelcolReady(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(60 * time.Second)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:13133/")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if err := sleepContext(ctx, 2*time.Second); err != nil {
			return err
		}
	}
	return context.DeadlineExceeded
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
