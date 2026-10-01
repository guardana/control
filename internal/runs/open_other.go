//go:build !unix

package runs

// nonBlocking has no counterpart here. The refusal that matters is the
// regular-file check, which needs no flag.
const nonBlocking = 0
