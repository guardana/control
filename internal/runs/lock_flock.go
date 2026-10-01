//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package runs

import (
	"errors"
	"os"
	"syscall"
)

// tryLock takes an exclusive lock on f without waiting, and reports false
// when another open file holds it. Closing f releases it, and so does the
// death of the process, so a crash leaves nothing that refuses the next
// writer.
func tryLock(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	}
	return false, err
}
