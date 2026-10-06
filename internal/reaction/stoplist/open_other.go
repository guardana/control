//go:build !unix

package stoplist

// OpenFlags are none here: OpenDir refuses every directory on a platform that
// keeps no permission bits, so no open of the list is reached.
const OpenFlags = 0
