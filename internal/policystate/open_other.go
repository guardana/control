//go:build !unix

package policystate

// nonBlocking has no counterpart here. The refusal that matters is the
// regular-file check, which needs no flag.
const nonBlocking = 0
