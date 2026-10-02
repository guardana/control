//go:build unix

package policystate

import "syscall"

// nonBlocking makes an open of a named pipe or a device return instead of
// waiting for the other end, so the regular-file check runs at all.
const nonBlocking = syscall.O_NONBLOCK
