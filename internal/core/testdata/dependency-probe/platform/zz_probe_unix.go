//go:build unix

package ioprobe

// DependencyProbeUnix is compiled on every platform the gate runs on and left
// out on the others: the constraint line and the foreign listing refuse it.
func DependencyProbeUnix() {}
