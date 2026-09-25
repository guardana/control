//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package approvals

import "io/fs"

// identify answers what the lock answers here: neither handle opens a
// directory on a platform with no file lock.
func identify(fs.FileInfo) (dirID, error) { return dirID{}, ErrNoLock }
