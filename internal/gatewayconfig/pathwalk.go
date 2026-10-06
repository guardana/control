package gatewayconfig

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/guardana/control/internal/files"
)

// maxLinks bounds the links one path may pass, as the kernel bounds its own
// lookup, so a loop of links is refused rather than followed.
const maxLinks = 40

// resolveJudged resolves path a component at a time from the root of the
// file system, following each link it meets, and judges every entry it
// passes: each directory and each link has to be the plane's account's or
// root's, and no directory may let others write it unless it is sticky or on
// a read-only mount. Whoever may change any of them could put a file of their
// own at the path. It returns the path the walk reached, which holds no link.
func resolveJudged(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	top, err := os.Lstat(separator)
	if err != nil {
		return "", err
	}
	if err := judgeTraversed(separator, top); err != nil {
		return "", err
	}
	w := &pathWalk{at: separator, isDir: true, pending: strings.Split(abs, separator)}
	for len(w.pending) > 0 {
		if err := w.step(); err != nil {
			return "", err
		}
	}
	return w.at, nil
}

const separator = string(filepath.Separator)

// pathWalk is a walk under way: the path reached, which holds no link,
// whether it is a directory, the links passed, and the names still to take.
type pathWalk struct {
	at      string
	isDir   bool
	links   int
	pending []string
}

// step takes the next name.
func (w *pathWalk) step() error {
	name := w.pending[0]
	w.pending = w.pending[1:]
	switch {
	case name == "" || name == ".":
		return nil
	case !w.isDir:
		return fmt.Errorf("%s: %w", w.at, files.ErrNotDirectory)
	case name == "..":
		w.at = filepath.Dir(w.at)
		return nil
	}
	next := filepath.Join(w.at, name)
	info, err := os.Lstat(next)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return w.follow(next, info)
	case info.IsDir():
		if err := judgeTraversed(next, info); err != nil {
			return err
		}
		w.at = next
	default:
		w.at, w.isDir = next, false
	}
	return nil
}

// follow puts the names link's target holds before the names still to take,
// from the root where the target is absolute.
func (w *pathWalk) follow(link string, info fs.FileInfo) error {
	if w.links++; w.links > maxLinks {
		return fmt.Errorf("%s: %w: more than %d", link, errTooManyLinks, maxLinks)
	}
	if err := checkConfigOwner(info); err != nil {
		return fmt.Errorf("the link %s: %w", link, err)
	}
	target, err := os.Readlink(link)
	if err != nil {
		return err
	}
	if filepath.IsAbs(target) {
		w.at = separator
	}
	w.pending = append(strings.Split(target, separator), w.pending...)
	return nil
}

// judgeTraversed judges a directory the path passes. A sticky directory lets
// nobody but an entry's owner replace the entry, and every entry the walk
// takes from it is judged by its owner in turn.
func judgeTraversed(dir string, info fs.FileInfo) error {
	if err := checkConfigOwner(info); err != nil {
		return fmt.Errorf("the directory %s: %w", dir, err)
	}
	mode := info.Mode()
	if mode&fs.ModeSticky != 0 || mode.Perm()&worldWritable == 0 {
		return nil
	}
	configStep(stepDirLooked)
	d, err := files.OpenDir(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	opened, err := d.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, opened) {
		return fmt.Errorf("the directory %s %w", dir, errChanged)
	}
	if err := judgeDirMode(d, opened); err != nil {
		return fmt.Errorf("the directory %s: %w", dir, err)
	}
	return nil
}

// judgeDirMode refuses an opened directory others may write, unless it is
// sticky or on a read-only mount, where no account may write it whatever its
// mode says. A mount whose flags cannot be read is refused.
func judgeDirMode(d *os.File, info fs.FileInfo) error {
	mode := info.Mode()
	if mode&fs.ModeSticky != 0 || mode.Perm()&worldWritable == 0 {
		return nil
	}
	ro, err := onReadOnlyMount(d)
	switch {
	case err != nil:
		return fmt.Errorf("%w: mode %04o, on a mount whose flags cannot be read: %w", files.ErrMode, mode.Perm(), err)
	case !ro:
		return fmt.Errorf("%w: mode %04o; others may not write it unless it is sticky or on a read-only mount", files.ErrMode, mode.Perm())
	}
	return nil
}
