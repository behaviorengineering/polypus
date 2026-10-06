//go:build !unix

package statefile

import (
	"fmt"
	"os"
)

func tryLock(f *os.File) error {
	return fmt.Errorf("file locking not supported on this platform")
}

func unlockFile(f *os.File) {}
