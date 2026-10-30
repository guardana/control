package segmentlog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/guardana/control/internal/files"
)

// forbidden is the permission bits neither the directory nor a file in it may
// have: whoever may write either can write evidence into the log.
const forbidden fs.FileMode = 0o022

// ownerWanted is the account the directory and its files belong to.
var ownerWanted = func(fs.FileInfo) int { return os.Geteuid() }

// openDir opens dir, creating it mode 0700 when absent, and refuses it unless
// it is a directory of this account that neither the group nor others may
// write. The checks read the opened directory, so the one judged is the one
// every name is opened in.
func openDir(dir string) (*os.Root, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: no directory", ErrConfig)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	// The trailing "." fails on anything but a directory rather than wait on
	// a named pipe put at dir.
	root, err := os.OpenRoot(dir + string(os.PathSeparator) + ".")
	if err != nil {
		return nil, err
	}
	info, err := root.Stat(".")
	if err == nil {
		err = judge(info, true)
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

// judge refuses what a writer of this package does not leave: anything but a
// directory or a regular file, as wanted, of this account, that neither the
// group nor others may write.
func judge(info fs.FileInfo, dir bool) error {
	switch {
	case dir && !info.IsDir():
		return fmt.Errorf("%w: not a directory", ErrDirectory)
	case !dir && !info.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", ErrDirectory, info.Name())
	case info.Mode().Perm()&forbidden != 0:
		return fmt.Errorf("%w: %s: %w: mode %04o", ErrDirectory, info.Name(), files.ErrMode, info.Mode().Perm())
	}
	if err := files.CheckOwnedBy(info, ownerWanted(info)); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrDirectory, info.Name(), err)
	}
	return nil
}

// openJudged opens name in root with flag, and refuses a file judge refuses,
// judged by its descriptor. A root follows a link that stays inside the
// directory whatever the flags say, so a link is refused by its directory
// entry before the open, and the descriptor has to be the file that entry
// named.
func openJudged(root *os.Root, name string, flag int) (*os.File, error) {
	before, err := root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist) && flag&os.O_CREATE != 0:
		before, flag = nil, flag|os.O_EXCL
	case err != nil:
		return nil, err
	case !before.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrDirectory, name)
	}
	f, err := root.OpenFile(name, flag|openFlags, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	switch {
	case err != nil:
	case before != nil && !os.SameFile(before, info):
		err = fmt.Errorf("%w: another file stood at %s when it was opened", ErrMoved, name)
	default:
		err = judge(info, false)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// names is every name in the directory root holds.
func names(root *os.Root) ([]string, error) {
	d, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	out, err := d.Readdirnames(-1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	return out, errors.Join(err, d.Close())
}

// readManifest reads the manifest in root, or reports it absent.
func readManifest(root *os.Root) ([]byte, bool, error) {
	f, err := root.OpenFile(ManifestName, os.O_RDONLY|openFlags, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	switch {
	case err != nil:
		return nil, false, err
	case !info.Mode().IsRegular():
		return nil, false, fmt.Errorf("%w: %s is not a regular file", ErrManifest, ManifestName)
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxManifestBytes+1))
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}
