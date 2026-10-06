//go:build unix

package observelog

import (
	"io/fs"
	"syscall"
)

// writeFlags keep an open from following a link at the file's name and from
// waiting on a named pipe before its type is judged.
const writeFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// singleName reports whether info's file has exactly one name. A platform
// that does not say has not shown that it does.
func singleName(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st != nil && st.Nlink == 1
}
