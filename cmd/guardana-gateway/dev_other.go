//go:build !unix

package main

import "os/exec"

// ownProcessGroup leaves cmd in dev's own group where the system has no
// process groups to start it in.
func ownProcessGroup(*exec.Cmd) {}
