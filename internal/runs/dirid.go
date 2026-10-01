package runs

import (
	"fmt"
	"io/fs"
)

// writableByOthers is the mode bits that would make a directory's runs a
// group's or the world's to open.
const writableByOthers fs.FileMode = 0o022

// dirID is what a directory is judged by: which directory it is, who owns it,
// and its permission bits.
type dirID struct {
	dev, ino uint64
	uid      uint32
	perm     fs.FileMode
}

// still refuses now unless it is the directory o describes, under the same
// owner, and writable by nobody else.
func (o dirID) still(now dirID) error {
	switch {
	case now.dev != o.dev || now.ino != o.ino:
		return fmt.Errorf("%w: another directory stands at its name", ErrDirectoryChanged)
	case now.uid != o.uid:
		return fmt.Errorf("%w: owned by uid %d, opened owned by uid %d", ErrDirectoryChanged, now.uid, o.uid)
	case now.perm&writableByOthers != 0:
		return fmt.Errorf("%w: %w: mode %04o", ErrDirectoryChanged, ErrPermissions, now.perm)
	}
	return nil
}
