package images

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsurePresentOK(t *testing.T) {
	dir := t.TempDir()
	path := writeImagesEnv(t, dir)

	err := EnsurePresent(context.Background(), path, EnsureOptions{
		Probe: func(ctx context.Context, ref string, token string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEnsurePresentFailFast(t *testing.T) {
	dir := t.TempDir()
	path := writeImagesEnv(t, dir)

	err := EnsurePresent(context.Background(), path, EnsureOptions{
		Wait: false,
		Probe: func(ctx context.Context, ref string, token string) error {
			return ErrManifestNotFound
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEnsurePresentWaitsThenOK(t *testing.T) {
	dir := t.TempDir()
	path := writeImagesEnv(t, dir)
	calls := 0

	err := EnsurePresent(context.Background(), path, EnsureOptions{
		Wait: true,
		Probe: func(ctx context.Context, ref string, token string) error {
			calls++
			if calls < 3 {
				return ErrManifestNotFound
			}
			return nil
		},
		WaitForBuild: func(ctx context.Context, version string, opts WaitOptions) error {
			return nil
		},
		PostWaitProbeInterval: 5 * time.Millisecond,
		PostWaitProbeTimeout:  500 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Fatalf("expected re-probe after wait, calls=%d", calls)
	}
}

func TestEnsurePresentPostWaitTimesOut(t *testing.T) {
	dir := t.TempDir()
	path := writeImagesEnv(t, dir)

	err := EnsurePresent(context.Background(), path, EnsureOptions{
		Wait: true,
		Probe: func(ctx context.Context, ref string, token string) error {
			return ErrManifestNotFound
		},
		WaitForBuild: func(ctx context.Context, version string, opts WaitOptions) error {
			return nil
		},
		PostWaitProbeInterval: 10 * time.Millisecond,
		PostWaitProbeTimeout:  30 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func writeImagesEnv(t *testing.T, dir string) string {
	path := filepath.Join(dir, "images.env")
	content := `POLYPUS_IMAGE=ghcr.io/behaviorengineering/polypus:1.0.0
SWITCHYARD_IMAGE=ghcr.io/behaviorengineering/polypus-switchyard:1.0.0
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
