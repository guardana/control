package ioprobe

import "os/exec"

// DependencyProbeFreeBSD runs a program in a FreeBSD build only, chosen by the
// file name with no constraint line. It is the only file of its package, so
// neither the gate's listing nor the foreign one names the package at all.
func DependencyProbeFreeBSD() *exec.Cmd { return exec.Command("true") }
