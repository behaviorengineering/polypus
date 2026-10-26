//go:build unix

package statefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

func TestLockFileTimesOutOnlyOnContention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	holder, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: test temp dir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	if err := tryLock(holder); err != nil {
		t.Fatal(err)
	}
	waiter, err := os.OpenFile(path, os.O_RDWR, 0o600) //nolint:gosec // G304: test temp dir
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = waiter.Close() }()
	var de *derrors.Error
	if err := lockFile(waiter, 50*time.Millisecond); !errors.As(err, &de) || de.Code() != derrors.CodeTimeout {
		t.Fatalf("a held lock must time out with CodeTimeout, got %v", err)
	}
}

func TestLockFileFailsFastOnOtherErrors(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close() // A closed descriptor makes flock fail with EBADF.
	start := time.Now()
	if err := lockFile(f, 5*time.Second); err == nil || errors.Is(err, errLockHeld) {
		t.Fatalf("want the underlying error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a non-contention error must not wait for the timeout")
	}
}
