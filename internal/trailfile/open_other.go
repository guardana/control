//go:build !unix

package trailfile

// No writer opens here, because the platform keeps no permission bits; the
// reader judges the file by its descriptor.
const (
	writeFlags = 0
	readFlags  = 0
)
