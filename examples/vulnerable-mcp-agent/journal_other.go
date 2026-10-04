//go:build !unix

package main

import "io/fs"

const journalFlags = 0

// singleName cannot count a file's names here, so no journal file is opened.
func singleName(fs.FileInfo) bool { return false }
