//go:build !unix

package files

import (
	"io/fs"
	"os"
)

const nonBlocking = 0

func openDir(string) (*os.File, error) { return nil, ErrNoDirOpen }

// PermissionBits is false here: the mode this platform reports is synthesized,
// so a check against it would pass without examining who can read the file.
const PermissionBits = false

// ownerOf names no owner here, so an owner check refuses.
func ownerOf(fs.FileInfo) (int, bool) { return 0, false }
