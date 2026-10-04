//go:build unix

package main

import (
	"io/fs"
	"syscall"
)

// journalFlags keep the journal's open from following a link at its name and
// from waiting on a named pipe before its type is judged.
const journalFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// singleName reports whether info's file has exactly one name.
func singleName(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st != nil && st.Nlink == 1
}
