//go:build unix

package holdjournal

import "syscall"

// nonBlocking makes an open of a named pipe or a device return instead of
// waiting for the other end. The journal holds one mutex for a whole
// operation, so an open that waits stops every later call as well; what the
// file turns out to be is settled by fstat before a byte of it is read.
const nonBlocking = syscall.O_NONBLOCK
