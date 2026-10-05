//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package findinglog

import "os"

// Without a file lock two writers could append to one log and interleave
// their lines, so Open refuses.
func lock(*os.File) error { return ErrNoLock }
