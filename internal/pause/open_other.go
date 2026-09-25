//go:build !unix

package pause

const openFlags = 0

// permissionBits is false here: the mode this platform reports is
// synthesized, so a check against it would pass without examining who may
// write the file, and every read is Unknown.
const permissionBits = false
