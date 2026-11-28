//go:build unix

package main

import (
	"os"
	"strconv"
	"syscall"

	ondisk "github.com/guardana/control/internal/files"
)

// ownerText names the account that owns the file at path, as a refusal
// says it.
func ownerText(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return "a file whose owner could not be read"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "a file whose owner could not be read"
	}
	return "owned by uid " + strconv.FormatUint(uint64(st.Uid), 10)
}

// ownedByCaller reports whether this process's effective account owns the
// file at path, not following a link.
func ownedByCaller(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && ondisk.CheckOwnedBy(info, os.Geteuid()) == nil
}
