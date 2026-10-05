package findinglog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guardana/control/internal/files"
)

// forbidden is the permission bits neither the directory nor the log file may
// have: findings name one account's runs, for that account alone to read or
// write.
const forbidden fs.FileMode = 0o077

// ownerWanted is the account info has to belong to: this process's, for the
// directory and for the log file alike.
var ownerWanted = func(fs.FileInfo) int { return os.Geteuid() }

// openDir opens dir and refuses it unless it is a directory of this account
// that the group and others cannot reach, and no link stands at its name. The
// open that refuses the link and the root the file is opened through are two
// opens of one path, so the root has to be the directory that was judged.
func openDir(dir string) (*os.Root, error) {
	if dir == "" {
		return nil, ErrPath
	}
	// A trailing separator would make the open follow a link at the name.
	dir = filepath.Clean(dir)
	d, err := files.OpenDir(dir)
	if err != nil {
		return nil, err
	}
	judged, err := d.Stat()
	if err = errors.Join(err, d.Close()); err != nil {
		return nil, err
	}
	if err := judgeDir(judged); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(judged, opened) {
		err = fmt.Errorf("%w: another directory stood at the name when it was opened", ErrChanged)
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func judgeDir(info fs.FileInfo) error {
	if info.Mode().Perm()&forbidden != 0 {
		return fmt.Errorf("%w: %w: mode %04o", ErrDirMode, files.ErrMode, info.Mode().Perm())
	}
	return checkOwner(info)
}

// openFile opens the log file in root to append to it, creating it when
// nothing is there. A root follows a symbolic link that stays inside the
// directory whatever the open's flags say, so a link is refused by its
// directory entry before the open, and the descriptor has to be the file that
// entry named.
func openFile(root *os.Root) (*os.File, error) {
	flag := os.O_RDWR | os.O_APPEND | openFlags
	before, err := root.Lstat(FileName)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		before, flag = nil, flag|os.O_CREATE|os.O_EXCL
	case err != nil:
		return nil, err
	case !before.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, before.Mode().Type())
	}
	f, err := root.OpenFile(FileName, flag, 0o600)
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

// judgeFile refuses an opened log file that is not a regular file of this
// account that the group and others cannot reach, with no name but the log's:
// a write the log's checks of its own name cannot see would reach a reader of
// the other.
func judgeFile(info fs.FileInfo) error {
	switch {
	case !info.Mode().IsRegular():
		return ErrNotRegular
	case info.Mode().Perm()&forbidden != 0:
		return fmt.Errorf("%w: mode %04o", ErrFileMode, info.Mode().Perm())
	case !singleName(info):
		return ErrLinks
	}
	return checkOwner(info)
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
