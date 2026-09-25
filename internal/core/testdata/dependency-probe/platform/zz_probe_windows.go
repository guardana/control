package ioprobe

import (
	"net"
	"os/exec"
)

// DependencyProbeWindows dials and runs a program in a Windows build only. A
// gate that looked at the build of the platform it runs on would never see it.
func DependencyProbeWindows() (*exec.Cmd, net.Dialer) {
	return exec.Command("true"), net.Dialer{}
}
