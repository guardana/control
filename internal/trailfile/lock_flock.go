//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package trailfile

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lock takes an exclusive lock on the open file. Closing the file releases
// it, and the kernel drops it when the process dies, so a collector that
// crashed leaves nothing behind that refuses the next one.
func lock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("locking the file: %w", err)
	}
	return nil
}

// heldByWriter reports whether a writer holds the file, by taking a shared
// lock without waiting and letting it go at once.
func heldByWriter(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("asking whether a writer holds the file: %w", err)
	}
	return false, syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
