//go:build plan9

package ioprobe

import "net"

// DependencyProbeOther dials in a build for a platform the gate never runs on,
// chosen by a build constraint rather than by the file name.
func DependencyProbeOther() net.Dialer { return net.Dialer{} }
