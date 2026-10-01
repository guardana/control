//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package runs

import "os"

// Without a file lock two writers of one root's state could each lose the
// other's raise, so nothing locks here: a second lock path would be one no
// test in this repository builds.
func tryLock(*os.File) (bool, error) { return false, ErrNoLock }
