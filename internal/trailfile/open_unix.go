//go:build unix

package trailfile

import "syscall"

// writeFlags keep the writer's open from following a link at the file's name
// and from waiting on a named pipe before its type is judged; readFlags keep
// the reader's from waiting.
const (
	writeFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	readFlags  = syscall.O_NONBLOCK
)
