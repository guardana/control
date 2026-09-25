//go:build unix

package approvals

import "syscall"

// nonBlocking makes an open of a named pipe or a device return instead of
// waiting for the other end. The store holds one mutex for a whole operation,
// so an open that waits stops every later call as well.
const nonBlocking = syscall.O_NONBLOCK
