//go:build unix

package gatewayconfig

import (
	"errors"
	"syscall"
)

// writeOK is access(2)'s W_OK, which every unix spells 2.
const writeOK = 0x2

// accessWrite asks the kernel whether the plane's account may write path,
// which the kernel answers for the real uid and groups, access control lists
// and read-only mounts included. A plane whose real and effective uids differ
// is not the account the kernel answers for, so it is refused.
func accessWrite(path string) (bool, error) {
	if syscall.Getuid() != syscall.Geteuid() {
		return false, errors.New("the plane's real and effective uids differ, and access(2) answers for the real one")
	}
	switch err := syscall.Access(path, writeOK); {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EROFS):
		return false, nil
	default:
		return false, err
	}
}
