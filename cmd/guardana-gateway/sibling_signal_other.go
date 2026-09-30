//go:build !unix

package main

import "os/exec"

// killedBy reports no signal where the system has no wait status to read one
// from; the exit status is reported instead.
func killedBy(*exec.ExitError, string) (string, bool) { return "", false }
