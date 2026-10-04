//go:build !unix

package observelog

import "io/fs"

// No writer opens here, because the platform keeps no permission bits; the
// export judges the file by its descriptor.
const (
	writeFlags = 0
	readFlags  = 0
)

// singleName cannot count a file's names here, so no file has shown it has
// one.
func singleName(fs.FileInfo) bool { return false }
