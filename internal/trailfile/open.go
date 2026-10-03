package trailfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guardana/control/internal/files"
)

// ownerWanted is the account info has to belong to: this process's, for the
// file and for its directory alike.
var ownerWanted = func(fs.FileInfo) int { return os.Geteuid() }

// fileName returns the last element of path, and refuses a path whose last
// element is no file name. filepath.Base drops trailing separators, so "x/"
// would otherwise open the file x inside the directory x.
func fileName(path string) (string, error) {
	name := filepath.Base(path)
	if path == "" || os.IsPathSeparator(path[len(path)-1]) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q", ErrPath, path)
	}
	return name, nil
}

// openDir opens dir and refuses it unless it is a directory of this account
// that neither the group nor others may write. The checks read the opened
// directory, so the one judged is the one the file is opened in.
func openDir(dir string) (*os.Root, error) {
	// The trailing "." fails on anything but a directory rather than wait on
	// a named pipe put at dir.
	root, err := os.OpenRoot(dir + string(os.PathSeparator) + ".")
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	switch {
	case err != nil:
	case info.Mode().Perm()&forbidden != 0:
		err = fmt.Errorf("%w: %w: mode %04o", ErrDirMode, files.ErrMode, info.Mode().Perm())
	default:
		err = checkOwner(info)
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

// openFile opens name in root to append to it, creating it when nothing is
// there. A root follows a symbolic link that stays inside the directory
// whatever the open's flags say, so a link is refused by its directory entry
// before the open, and the descriptor has to be the file that entry named.
func openFile(root *os.Root, name string) (*os.File, error) {
	flag := os.O_RDWR | os.O_APPEND | writeFlags
	before, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		before, flag = nil, flag|os.O_CREATE|os.O_EXCL
	case err != nil:
		return nil, err
	case !before.Mode().IsRegular():
		return nil, ErrNotRegular
	}
	f, err := root.OpenFile(name, flag, 0o600)
	if err != nil {
		return nil, err
	}
	if before != nil {
		after, err := f.Stat()
		if err == nil && !os.SameFile(before, after) {
			err = fmt.Errorf("%w: another file stood at the name when it was opened", ErrChanged)
		}
		if err != nil {
			return nil, errors.Join(err, f.Close())
		}
	}
	return f, nil
}

// checkOwner refuses a file or directory this account does not own, and one
// whose owner the platform does not name.
func checkOwner(info fs.FileInfo) error {
	if err := files.CheckOwnedBy(info, ownerWanted(info)); err != nil {
		return fmt.Errorf("%w: %w", ErrOwner, err)
	}
	return nil
}

func syncRoot(root *os.Root) error {
	d, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// judgeFile refuses an opened file that is not a regular file owned by this
// account that neither the group nor others may write.
func judgeFile(info fs.FileInfo) error {
	switch {
	case !info.Mode().IsRegular():
		return ErrNotRegular
	case info.Mode().Perm()&forbidden != 0:
		return fmt.Errorf("%w: mode %04o", ErrFileMode, info.Mode().Perm())
	}
	return checkOwner(info)
}
