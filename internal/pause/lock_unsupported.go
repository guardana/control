//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package pause

import (
	"context"
	"os"
)

// Without a file lock two writers could each read the document, change it and
// write it back, and one change would be lost without a word. The writer
// refuses instead.
func lockDir(context.Context, *os.Root) (func() error, error) { return nil, ErrNoLock }
