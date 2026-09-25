//go:build unix

package pause

import "syscall"

// openFlags keep an open from following a link planted after the check that
// refused one, and from waiting on a named pipe before its type is judged.
const openFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK

// permissionBits reports whether this platform keeps the permission bits the
// reader checks.
const permissionBits = true
