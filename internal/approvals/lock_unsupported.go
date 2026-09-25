//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package approvals

import "os"

// Without a file lock there is no way to keep two planes out of one directory,
// and no way for an approver to tell whether anything will consume its answer.
// Both handles refuse to open rather than serve a directory nothing holds: a
// second lock path here would be one no test in this repository builds.

func lockDir(*os.Root, string) (*os.File, error) { return nil, ErrNoLock }

func planeHoldsLock(*os.Root, string) (bool, error) { return false, ErrNoLock }
