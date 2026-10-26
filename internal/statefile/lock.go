package statefile

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// lockPath returns the lock file path for a state file.
func lockPath(path string) string {
	return path + ".lock"
}

// WithFileLock runs fn while holding an exclusive lock on path.lock.
func WithFileLock(path string, fn func() error) error {
	lockPath := lockPath(path)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WithFileLock", "mkdir")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WithFileLock", "open lock")
	}
	defer func() { _ = f.Close() }()
	if err := lockFile(f, lockWait); err != nil {
		return derrors.Wrap(err, derrors.CodeOf(err), "statefile.WithFileLock", "lock").With("path", lockPath)
	}
	defer unlockFile(f)
	return fn()
}

// lockWait is how long WithFileLock waits for another holder to release the lock.
const lockWait = 30 * time.Second

// errLockHeld is tryLock's answer when another process holds the lock; only that
// is worth waiting on.
var errLockHeld = errors.New("statefile: lock held by another process")

// lockFile retries while the lock is held elsewhere, up to wait. Any other failure
// (bad descriptor, no lock support) returns at once with its cause.
func lockFile(f *os.File, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := tryLock(f)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errLockHeld) {
			return derrors.Wrap(err, derrors.CodeInternal, "statefile.lockFile", "lock")
		}
		if time.Now().After(deadline) {
			return derrors.Wrap(err, derrors.CodeTimeout, "statefile.lockFile", "lock timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
