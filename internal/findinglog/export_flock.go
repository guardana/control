//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package findinglog

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// heldByWriter reports whether a writer holds the log's exclusive lock. The
// shared lock it tries is released at once; it is taken only when the log
// holds bytes past its last committed write.
func heldByWriter(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	switch {
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("asking whether a writer holds the log: %w", err)
	}
	return false, syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
