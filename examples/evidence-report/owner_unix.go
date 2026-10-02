//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"io/fs"
	"syscall"
)

// ownerOf reads the uid that owns the file info describes.
func ownerOf(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
