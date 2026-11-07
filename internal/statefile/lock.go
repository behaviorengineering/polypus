package statefile

import (
	"context"
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

// WithFileLock runs fn while holding an exclusive lock on path.lock. It waits up
// to lockWait for another holder, and stops waiting as soon as ctx is done, so an
// HTTP handler whose client went away does not keep polling.
func WithFileLock(ctx context.Context, path string, fn func() error) error {
	const op = "statefile.WithFileLock"
	if ctx == nil || fn == nil {
		return derrors.New(derrors.CodeInvalid, op, "context and fn are required")
	}
	lp := lockPath(path)
	if err := os.MkdirAll(filepath.Dir(lp), 0o700); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "mkdir")
	}
	f, err := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: a path the operator configures (env or flag), not request input
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, op, "open lock")
	}
	defer func() { _ = f.Close() }()
	if err := lockFile(ctx, f, lockWait); err != nil {
		return derrors.Wrap(err, derrors.CodeOf(err), op, "lock").With("path", lp)
	}
	defer unlockFile(f)
	return fn()
}

// lockWait is how long WithFileLock waits for another holder to release the lock.
const lockWait = 30 * time.Second

// lockPoll is how often lockFile retries a held lock.
const lockPoll = 10 * time.Millisecond

// errLockHeld is tryLock's answer when another process holds the lock; only that
// is worth waiting on.
var errLockHeld = errors.New("statefile: lock held by another process")

// lockFile retries while the lock is held elsewhere, up to wait or until ctx is
// done. Any other failure (bad descriptor, no lock support) returns at once with
// its cause.
func lockFile(ctx context.Context, f *os.File, wait time.Duration) error {
	const op = "statefile.lockFile"
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	t := time.NewTicker(lockPoll)
	defer t.Stop()
	for {
		err := tryLock(f)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errLockHeld) {
			return derrors.Wrap(err, derrors.CodeInternal, op, "lock")
		}
		select {
		case <-ctx.Done():
			code := derrors.CodeTimeout
			if errors.Is(ctx.Err(), context.Canceled) {
				code = derrors.CodeCanceled
			}
			return derrors.Wrap(errors.Join(err, ctx.Err()), code, op, "lock wait ended")
		case <-t.C:
		}
	}
}
