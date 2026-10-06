//go:build unix

package stoplist

import "syscall"

// OpenFlags are added to every open of the stop list, the writer's too: they
// keep it from following a link planted after the check that refused one,
// and from waiting on a named pipe before its type is judged.
const OpenFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
