//go:build unix

package spool

import "syscall"

// noFollow refuses a symbolic link at the name an open creates or appends to.
const noFollow = syscall.O_NOFOLLOW
