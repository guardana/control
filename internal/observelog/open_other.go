//go:build !unix

package observelog

import "io/fs"

// Nothing opens a log here, because the platform keeps no permission bits.
const writeFlags = 0

// singleName cannot count a file's names here, so no file has shown it has
// one.
func singleName(fs.FileInfo) bool { return false }
