//go:build unix

package statefile

import (
	"context"
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
	if err := lockFile(context.Background(), waiter, 50*time.Millisecond); !errors.As(err, &de) || de.Code() != derrors.CodeTimeout {
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
	if err := lockFile(context.Background(), f, 5*time.Second); err == nil || errors.Is(err, errLockHeld) {
		t.Fatalf("want the underlying error, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a non-contention error must not wait for the timeout")
	}
}

func TestLockFileStopsWhenContextIsCanceled(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	err = lockFile(ctx, waiter, 5*time.Second)
	if derrors.CodeOf(err) != derrors.CodeCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled wait must end with CodeCanceled, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a canceled context must stop the wait before the lock timeout")
	}
}

func TestWithFileLockRejectsNilContext(t *testing.T) {
	//nolint:staticcheck // SA1012: the nil context is the case under test.
	err := WithFileLock(nil, filepath.Join(t.TempDir(), "state.json"), func() error { return nil })
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("want CodeInvalid for a nil context, got %v", err)
	}
}

func TestWithFileLockRejectsNilFn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	err := WithFileLock(context.Background(), path, nil)
	if derrors.CodeOf(err) != derrors.CodeInvalid {
		t.Fatalf("want CodeInvalid for a nil fn, got %v", err)
	}
	if _, statErr := os.Stat(lockPath(path)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("a nil fn must be refused before the lock file is made: %v", statErr)
	}
}
