//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package stopwrite

import (
	"context"
	"os"
)

// Without a file lock two writers could each judge the list without the
// other's line and both append, naming one finding twice. The writer refuses
// instead.
func lockDir(context.Context, *os.Root) (func() error, error) { return nil, ErrNoLock }
