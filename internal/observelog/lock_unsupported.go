//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package observelog

import "os"

// Without a file lock two writers could append to one log and interleave
// their lines, so Open refuses.
func lock(*os.File) error { return ErrNoLock }

// No writer of this package opens a file here, so none holds one.
func heldByWriter(*os.File) (bool, error) { return false, nil }
