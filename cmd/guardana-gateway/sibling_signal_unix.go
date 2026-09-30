//go:build unix

package main

import (
	"os/exec"
	"runtime"
	"syscall"
)

// killedBy names the signal that ended a sibling in dir, with what to do
// about it where there is something to do, or reports that no signal did.
func killedBy(exit *exec.ExitError, dir string) (string, bool) {
	ws, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return "", false
	}
	return ws.Signal().String() + killedHint(runtime.GOOS, ws.Signal(), dir), true
}

// killedHint is what to add to a run of a sibling in dir that goos killed
// with sig: macOS kills at its start a downloaded binary it has quarantined,
// and says nothing else about it.
func killedHint(goos string, sig syscall.Signal, dir string) string {
	if goos != "darwin" || sig != syscall.SIGKILL {
		return ""
	}
	return "; macOS kills a downloaded binary it has quarantined: once you have verified the archive, run xattr -dr com.apple.quarantine " +
		shellWord(dir) + " and start again"
}
