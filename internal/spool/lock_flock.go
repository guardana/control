//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package spool

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile is the name a lock takes under the directory; flock needs none.
const lockFile = ""

// lockDir takes an exclusive lock on the directory itself and returns what
// releases it. The kernel drops the lock when the process dies, so a crash
// leaves nothing behind that refuses the next Open.
func lockDir(dir string) (func() error, error) {
	d, err := os.Open(dir) //nolint:gosec // G304: the operator's spool directory, checked at Open
	if err != nil {
		return nil, fmt.Errorf("spool: %w", err)
	}
	if err := syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("%w: %q", ErrLocked, dir), d.Close())
		}
		return nil, errors.Join(fmt.Errorf("spool: locking %q: %w", dir, err), d.Close())
	}
	return d.Close, nil
}
