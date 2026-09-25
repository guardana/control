//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package spool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// lockFile is the file that holds the directory on a platform without flock.
const lockFile = "spool.lock"

// lockDir creates the lock file exclusively, naming the process that holds it,
// and returns what removes it. One a dead process left behind is refused by
// name: whether its holder is gone is the operator's call, not the spool's.
func lockDir(dir string) (func() error, error) {
	path := filepath.Join(dir, lockFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the spool's own lock file under the directory checked at Open
	if errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("%w: %s exists; remove it once no process holds the spool", ErrLocked, path)
	}
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	_, err = fmt.Fprintf(f, "%d\n", os.Getpid())
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return nil, errors.Join(fmt.Errorf("spool: %w", err), os.Remove(path))
	}
	return func() error { return os.Remove(path) }, nil
}
