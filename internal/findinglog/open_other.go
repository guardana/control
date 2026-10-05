//go:build !unix

package findinglog

import "io/fs"

// Nothing opens here, because the platform keeps no permission bits.
const openFlags = 0

// singleName cannot count a file's names here, so no file has shown it has
// one.
func singleName(fs.FileInfo) bool { return false }
