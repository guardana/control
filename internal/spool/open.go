package spool

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// segmentFile is what the spool writes through: an appending file that can be
// forced to disk, cut back and described. The operating system's file is the
// one outside tests.
type segmentFile interface {
	io.Writer
	Sync() error
	Close() error
	Truncate(size int64) error
	Stat() (fs.FileInfo, error)
}

// openOSFile opens name in root with flag. The mode keeps the evidence to the
// process's own user.
func openOSFile(root *os.Root, name string, flag int) (segmentFile, error) {
	return root.OpenFile(name, flag, 0o600)
}

// openOSRead opens name in root to read it.
func openOSRead(root *os.Root, name string, flag int) (*os.File, error) {
	return root.OpenFile(name, flag, 0)
}

// openForAppend opens the spool's file name, called what, to append to it or
// cut it back. With no file known there it is created, and refused when the
// name is taken; with one known, the name has to lead to that file still.
func (s *Spool) openForAppend(name, what string, known fs.FileInfo) (segmentFile, fs.FileInfo, error) {
	flag := os.O_WRONLY | os.O_APPEND | nonBlock
	if known == nil {
		flag |= os.O_CREATE | os.O_EXCL
	} else if err := s.lookBefore(name, what, known); err != nil {
		return nil, nil, err
	}
	f, err := s.opts.openFile(s.root, name, flag)
	if errors.Is(err, fs.ErrExist) {
		return nil, nil, fmt.Errorf("%w: %s already exists", ErrCorrupt, what)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("spool: %s: %w", what, err)
	}
	info, err := checkOpened(f, what, known)
	if err != nil {
		if known == nil {
			return nil, nil, errors.Join(err, f.Close(), s.root.Remove(name))
		}
		return nil, nil, errors.Join(err, f.Close())
	}
	return f, info, nil
}

// openForRead opens the spool's file name, called what, to read it, refusing
// what lookBefore and checkOpened refuse.
func (s *Spool) openForRead(name, what string, known fs.FileInfo) (*os.File, fs.FileInfo, error) {
	if err := s.lookBefore(name, what, known); err != nil {
		return nil, nil, err
	}
	f, err := s.opts.openRead(s.root, name, os.O_RDONLY|nonBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("spool: %s: %w", what, err)
	}
	info, err := checkOpened(f, what, known)
	if err != nil {
		return nil, nil, errors.Join(err, f.Close())
	}
	return f, info, nil
}

// lookBefore refuses a name that no longer holds the file known there. A
// root follows a symbolic link that stays inside the directory whatever the
// open's flags say, so a link at the name is refused here, by its entry.
func (s *Spool) lookBefore(name, what string, known fs.FileInfo) error {
	before, err := s.root.Lstat(name)
	switch {
	case err != nil:
		return fmt.Errorf("spool: %s: %w", what, err)
	case !before.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", ErrForeignFile, what)
	case known == nil || !os.SameFile(known, before):
		return fmt.Errorf("%w: %s is not the file the spool found there", ErrForeignFile, what)
	}
	return nil
}

// cutBack truncates the spool's file name to size through a descriptor judged
// to be the file known there, so a link put at the name since is never what
// is cut.
func (s *Spool) cutBack(name, what string, known fs.FileInfo, size int64) error {
	f, _, err := s.openForAppend(name, what, known)
	if err != nil {
		return err
	}
	if err := errors.Join(f.Truncate(size), f.Close()); err != nil {
		return fmt.Errorf("spool: cutting %s back: %w", what, err)
	}
	return nil
}

// checkOpened describes the file an open of what returned and refuses it with
// ErrForeignFile unless it is a regular file with one name and, when a file is
// known there, that file. A second name lets whoever holds it change the
// evidence behind the spool's back, or read what the spool appends; the
// refusal says where the file is, so the other name can be found.
func checkOpened(f interface{ Stat() (fs.FileInfo, error) }, what string, known fs.FileInfo) (fs.FileInfo, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("spool: %s: %w", what, err)
	}
	single, where, counted := linkState(info)
	switch {
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrForeignFile, what)
	case !counted:
		return nil, fmt.Errorf("spool: %s: the platform reports no link count", what)
	case !single:
		return nil, fmt.Errorf("%w: %s has a name other than its own, or none (%s)", ErrForeignFile, what, where)
	case known != nil && !os.SameFile(known, info):
		return nil, fmt.Errorf("%w: %s is not the file the spool found there (opened %s)", ErrForeignFile, what, where)
	}
	return info, nil
}
