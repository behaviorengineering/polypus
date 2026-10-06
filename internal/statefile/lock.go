package statefile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// LockPath returns the lock file path for a state file.
func LockPath(path string) string {
	return path + ".lock"
}

// WithFileLock runs fn while holding an exclusive lock on path.lock.
func WithFileLock(path string, fn func() error) error {
	lockPath := LockPath(path)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WithFileLock", "mkdir")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WithFileLock", "open lock")
	}
	defer func() { _ = f.Close() }()
	if err := lockFile(f); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WithFileLock", "lock")
	}
	defer unlockFile(f)
	return fn()
}

func lockFile(f *os.File) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := tryLock(f); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lock timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
