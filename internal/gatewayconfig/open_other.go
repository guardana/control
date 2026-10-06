//go:build !unix

package gatewayconfig

import "io/fs"

// openFlags is unused here: files.PermissionBits is false, so the loader
// refuses before it opens anything.
const openFlags = 0

// statOwner names no owner here, so every owner check refuses.
func statOwner(fs.FileInfo) (int, bool) { return 0, false }

// singleName cannot count a file's names here, so no file has shown it has
// one.
func singleName(fs.FileInfo) bool { return false }
