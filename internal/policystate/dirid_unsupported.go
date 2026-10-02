//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package policystate

import "io/fs"

// identify answers what the lock answers here: nothing opens a floor
// directory on a platform with no file lock.
func identify(fs.FileInfo) (fileID, error) { return fileID{}, ErrNoLock }
