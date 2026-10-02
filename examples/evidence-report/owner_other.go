//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd)

package main

import "io/fs"

// ownerOf answers that the owner cannot be read here, so a state directory
// is refused rather than taken unjudged.
func ownerOf(fs.FileInfo) (int, bool) { return 0, false }
