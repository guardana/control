//go:build !unix

package spool

import "io/fs"

// nonBlock is empty here: the platform keeps no permission bits, so Open
// refuses the directory before any file in it is opened.
const nonBlock = 0

// linkState names no link count here, so every open of a segment refuses.
func linkState(fs.FileInfo) (single bool, where string, known bool) { return false, "", false }
