//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package holdjournal

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockDir takes an exclusive lock on the directory itself and returns what
// releases it. The kernel drops the lock when the process dies, so a plane
// that crashed leaves nothing behind that refuses the next one.
func lockDir(dir string) (func() error, error) {
	d, err := os.Open(dir) //nolint:gosec // G304: the directory checked at Open
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotAJournal, err)
	}
	if err := syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("%w: %q", ErrLocked, dir), d.Close())
		}
		return nil, errors.Join(fmt.Errorf("%w: locking %q: %w", ErrNotAJournal, dir, err), d.Close())
	}
	return d.Close, nil
}
