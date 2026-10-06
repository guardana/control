//go:build unix

package gatewayconfig

import "syscall"

// openFlags keep the open of the file named in the judged directory from
// following a link put there after it was named, and from waiting on a named
// pipe before its type is judged.
const openFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
