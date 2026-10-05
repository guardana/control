//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package findinglog

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lock takes an exclusive lock on the open log file. Closing the file
// releases it, and the kernel drops it when the process dies, so a writer
// that crashed leaves nothing behind that refuses the next one.
func lock(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrLocked
		}
		return fmt.Errorf("locking the log file: %w", err)
	}
	return nil
}
