//go:build !unix

package approvals

// nonBlocking has no counterpart here. The refusal that matters is the one in
// the directory listing, which needs no flag.
const nonBlocking = 0
