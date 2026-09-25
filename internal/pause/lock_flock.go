//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package pause

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockRetry is how often a writer tries the lock again while another holds it.
const lockRetry = 10 * time.Millisecond

// lockDir takes an exclusive lock on the directory root holds, trying until
// ctx ends, and returns what releases it. The kernel drops the lock when the
// process dies, so a writer that crashed leaves nothing behind that refuses the
// next one.
func lockDir(ctx context.Context, root *os.Root) (func() error, error) {
	dir := root.Name()
	d, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return d.Close, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("locking %q: %w", dir, err), d.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("%w: %q: %w", ErrLocked, dir, ctx.Err()), d.Close())
		case <-time.After(lockRetry):
		}
	}
}
