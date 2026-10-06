//go:build linux || darwin

package gatewayconfig

import (
	"os"
	"syscall"
)

// mountReadOnly is the flag statfs sets on a file system mounted read-only:
// ST_RDONLY on Linux and MNT_RDONLY on Darwin, which share the value.
const mountReadOnly = 0x1

// readOnlyMount reports whether the opened directory d sits on a file system
// mounted read-only, where no account may write it whatever its mode says.
func readOnlyMount(d *os.File) (bool, error) {
	raw, err := d.SyscallConn()
	if err != nil {
		return false, err
	}
	var st syscall.Statfs_t
	var statErr error
	if err := raw.Control(func(fd uintptr) {
		statErr = syscall.Fstatfs(int(fd), &st)
	}); err != nil {
		return false, err
	}
	if statErr != nil {
		return false, statErr
	}
	return int64(st.Flags)&mountReadOnly != 0, nil
}
