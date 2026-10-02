//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package policystate

import (
	"fmt"
	"io/fs"
	"syscall"
)

// identify reads which file info describes and who owns it.
func identify(info fs.FileInfo) (fileID, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, fmt.Errorf("%w: the owner cannot be read", ErrNotStateDir)
	}
	return fileID{dev: deviceNumber(st.Dev), ino: st.Ino, uid: st.Uid, perm: info.Mode().Perm()}, nil
}

// deviceNumber widens a device number to the type fileID keeps. Its type
// differs by platform, and a device number is never negative.
func deviceNumber[T int32 | uint32 | uint64](dev T) uint64 {
	return uint64(dev)
}
