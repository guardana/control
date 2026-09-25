//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in a process group of its own, which the
// terminal's interrupt does not reach: dev stops it by closing its input.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
