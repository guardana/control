//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package approvals

import (
	"fmt"
	"io/fs"
	"syscall"
)

// identify reads which directory info describes and who owns it.
func identify(info fs.FileInfo) (dirID, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return dirID{}, fmt.Errorf("%w: the directory's owner cannot be read", ErrNotAStore)
	}
	return dirID{dev: deviceNumber(st.Dev), ino: st.Ino, uid: st.Uid, perm: info.Mode().Perm()}, nil
}

// deviceNumber widens a device number to the type dirID keeps. Its type
// differs by platform, and a device number is never negative.
func deviceNumber[T int32 | uint32 | uint64](dev T) uint64 {
	return uint64(dev)
}
