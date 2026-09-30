//go:build !plan9

package ioprobe

// DependencyProbeNotPlan9 is compiled on every platform a gate machine or its
// foreign listing builds for, so no listing leaves it out: only the constraint
// line itself shows that another platform builds the package without it.
func DependencyProbeNotPlan9() {}
