package statefile

import (
	"os"
	"path/filepath"

	derrors "github.com/behaviorengineering/polypus/internal/errors"
)

// WriteAtomic writes content to path with a temp file in the same directory, synced
// before the rename so a crash leaves the old file or the new one, never a torn one.
func WriteAtomic(path string, content []byte, fileMode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "mkdir")
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "temp")
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(fileMode); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "chmod")
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "write")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "sync")
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "close")
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return derrors.Wrap(err, derrors.CodeInternal, "statefile.WriteAtomic", "rename")
	}
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable where the platform supports syncing a
// directory. Best effort: the data file itself is already synced.
func syncDir(dir string) {
	d, err := os.Open(dir) //nolint:gosec // G304: the directory of a path the caller chose
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	_ = d.Sync() //nolint:errcheck // not supported on every platform (Windows); the rename stands
}
