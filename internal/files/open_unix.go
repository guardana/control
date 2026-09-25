//go:build unix

package files

import (
	"io/fs"
	"os"
	"syscall"
)

// nonBlocking makes an open of a named pipe or a device return instead of
// waiting for the other end, so the regular-file check runs at all.
const nonBlocking = syscall.O_NONBLOCK

// PermissionBits reports whether this platform keeps the permission bits a
// forbidden mask is checked against.
const PermissionBits = true

func openDir(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|nonBlocking, 0) //nolint:gosec // G304: the caller's path, which the flags refuse unless it is a directory
}

// ownerOf is the uid that owns info's file, when the platform says.
func ownerOf(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return 0, false
	}
	return int(st.Uid), true
}
