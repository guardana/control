//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package approvals

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockDir takes an exclusive lock on the directory root holds and returns
// the handle the lock is on; closing it releases the lock. The kernel drops
// the lock when the process dies, so a plane that crashed leaves nothing
// behind that refuses the next one. dir names the directory in a refusal.
func lockDir(root *os.Root, dir string) (*os.File, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotAStore, err)
	}
	if err := syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("%w: %q", ErrLocked, dir), d.Close())
		}
		return nil, errors.Join(fmt.Errorf("%w: locking %q: %w", ErrNotAStore, dir, err), d.Close())
	}
	return d, nil
}

// planeHoldsLock reports whether a plane holds the directory root holds. It
// answers by trying the lock and releasing it at once, which is the only way
// to ask: a plane starting inside that window sees the directory held and
// refuses to start, which is loud, retryable and the closed direction.
func planeHoldsLock(root *os.Root, dir string) (bool, error) {
	d, err := root.Open(".")
	if err != nil {
		return false, fmt.Errorf("%w: %w", ErrNotAStore, err)
	}
	err = syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return true, d.Close()
	}
	if err != nil {
		return false, errors.Join(fmt.Errorf("%w: probing %q: %w", ErrNotAStore, dir, err), d.Close())
	}
	return false, d.Close()
}
