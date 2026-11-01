//go:build unix

package segmentlog

import "syscall"

// openFlags keep an open from following a link at a name and from waiting on
// a named pipe before its type is judged.
const openFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
