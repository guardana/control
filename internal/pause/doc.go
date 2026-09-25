// Package pause reads and writes the operator's pause file and serves the
// state a plane decides every call under (ADR-0019). The file is one strict
// JSON document of entries, each pausing a scope; only this package writes
// it, under an exclusive lock, and a plane reads it into an immutable
// snapshot whose zero value is unknown, because a state nobody read is not a
// clear one.
package pause
