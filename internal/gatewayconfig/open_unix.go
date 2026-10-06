//go:build unix

package gatewayconfig

import (
	"io/fs"
	"syscall"
)

// openFlags keep the open of the file named in the judged directory from
// following a link put there after it was named, and from waiting on a named
// pipe before its type is judged.
const openFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// statOwner is the uid that owns info's file.
func statOwner(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return int(st.Uid), true
}

// singleName reports whether info's file has exactly one name.
func singleName(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st != nil && st.Nlink == 1
}
