package images

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestEnsurePresentOK(t *testing.T) {
	dir := t.TempDir()
	path := writeImagesEnv(t, dir)

	err := EnsurePresent(context.Background(), path, EnsureOptions{
		Probe: func(ctx context.Context, ref string) error {
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
		Probe: func(ctx context.Context, ref string) error {
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
		Probe: func(ctx context.Context, ref string) error {
			calls++
			if calls < 3 {
				return ErrManifestNotFound
			}
			return nil
		},
		WaitForBuild: func(ctx context.Context, version string, opts WaitOptions) error {
			return nil
		},
		WaitTimeout:           time.Second,
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
		Probe: func(ctx context.Context, ref string) error {
			return ErrManifestNotFound
		},
		WaitForBuild: func(ctx context.Context, version string, opts WaitOptions) error {
			return nil
		},
		WaitTimeout:           time.Second,
		PostWaitProbeInterval: 10 * time.Millisecond,
		PostWaitProbeTimeout:  30 * time.Millisecond,
	})
	if derrors.CodeOf(err) != derrors.CodeNotFound {
		t.Fatalf("err = %v, want the manifest still missing", err)
	}
}

// TestEnsurePresentWaitGetsItsOwnBudget checks the wait runs under a context
// bounded by WaitTimeout, not a deadline of its own.
func TestEnsurePresentWaitGetsItsOwnBudget(t *testing.T) {
	path := writeImagesEnv(t, t.TempDir())
	var left time.Duration
	err := EnsurePresent(testContext(t), path, EnsureOptions{
		Wait:        true,
		WaitTimeout: 3 * time.Second,
		Probe: func(ctx context.Context, ref string) error {
			return ErrManifestNotFound
		},
		WaitForBuild: func(ctx context.Context, version string, opts WaitOptions) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("wait context has no deadline")
			}
			left = time.Until(deadline)
			return errors.New("stop here")
		},
		PostWaitProbeInterval: time.Millisecond,
		PostWaitProbeTimeout:  time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected the wait's error")
	}
	if left <= 0 || left > 3*time.Second {
		t.Fatalf("wait budget left %v, want at most WaitTimeout", left)
	}
}

func TestEnsurePresentWaitNeedsATimeout(t *testing.T) {
	path := writeImagesEnv(t, t.TempDir())
	err := EnsurePresent(testContext(t), path, EnsureOptions{
		Wait:                  true,
		Probe:                 func(context.Context, string) error { return nil },
		PostWaitProbeInterval: time.Millisecond,
		PostWaitProbeTimeout:  time.Millisecond,
	})
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("err = %v, want a zero wait timeout refused", err)
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
