//go:build unix

package spool

import (
	"fmt"
	"io/fs"
	"syscall"
)

// nonBlock makes an open of a named pipe or a device return instead of waiting
// for the other end, so the check of what was opened runs at all.
const nonBlock = syscall.O_NONBLOCK

// linkState reports whether info's file has exactly one name, where the file
// is, and whether the platform said.
func linkState(info fs.FileInfo) (single bool, where string, known bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st == nil {
		return false, "", false
	}
	return st.Nlink == 1, fmt.Sprintf("device %d, inode %d, %d names", st.Dev, st.Ino, st.Nlink), true
}
