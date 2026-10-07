//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package findinglog

import "os"

// Without a file lock Open refuses every writer, so none holds the log.
func heldByWriter(*os.File) (bool, error) { return false, nil }
