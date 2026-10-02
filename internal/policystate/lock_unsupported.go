//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package policystate

import "os"

// Without a file lock two raisers of one floor could each write over the
// other's raise, so nothing locks here: a second lock path would be one no
// test in this repository builds.
func tryLock(*os.File) (bool, error) { return false, ErrNoLock }
