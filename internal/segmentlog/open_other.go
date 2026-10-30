//go:build !unix

package segmentlog

// No writer opens here, because the platform keeps no permission bits; a
// reader judges each file by its descriptor.
const openFlags = 0
