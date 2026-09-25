// Package core decides: one validated request against one policy snapshot, in
// the fixed order and under the fail-closed table ADR-0012 states. New checks
// the configuration once; Decide runs that order against one snapshot and says
// what to enforce.
//
// The rule, as the gate enforces it for this tree and the other guarded ones:
// a package may import this module outside adapters/ and the storage, server,
// gateway and ingest trees; google.golang.org/protobuf; and a named set of
// standard library packages that holds no network, file, process, system call,
// randomness, unsafe or plugin package. The functions in that set that read
// the clock, standard input, the zone database or the system's randomness are
// refused by name where a name reveals the read; the local zone behind a
// Format call is not, and the tests here hold it. A package in these trees
// builds from the same Go files on every platform. That is what makes a
// decision replayable from its recorded inputs as far as imports go. Each
// allowed package is trusted whole, and an interface a caller implements can
// still do I/O, which is how the core is meant to reach anything outside
// itself.
package core
