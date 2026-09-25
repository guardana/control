//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package trailfile

import "os"

// Without a file lock two collectors could append to one file and interleave
// their lines, so the writer refuses to open.
func lock(*os.File) error { return ErrNoLock }

// No writer of this package opens a file here, so none holds one.
func heldByWriter(*os.File) (bool, error) { return false, nil }
