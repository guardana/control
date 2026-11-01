//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package segmentlog

import "os"

// Without a file lock two writers could seal or prune one directory at once,
// so the writer refuses to open.
func lock(*os.File) error { return ErrNoLock }

// No writer of this package opens a directory here, so none holds one.
func heldByWriter(*os.File) (bool, error) { return false, nil }
