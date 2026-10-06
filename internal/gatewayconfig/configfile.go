package gatewayconfig

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guardana/control/internal/files"
)

// writableByOthers is the permission bit that lets any account replace or
// rewrite the configuration, which names the mode, the policy keys, the pause
// file and the credentials the plane sends. The group's write bit is taken,
// since a umask of 002 sets it on every file extracted or checked out.
const writableByOthers fs.FileMode = 0o002

// ownerWanted is the plane's account, which may own the configuration and
// its directory beside root.
var ownerWanted = func(fs.FileInfo) int { return os.Geteuid() }

// The steps a test can act at: the directory opened and judged, and the
// file's name judged no link.
const (
	stepDirOpened = "directory opened"
	stepFileNamed = "file named"
)

// configSteps, when a test sets it, runs at each step, so the test can swap
// the directory or the file under the path in between.
var configSteps func(step string)

func configStep(s string) {
	if configSteps != nil {
		configSteps(s)
	}
}

// errChanged is a directory or file that is not, by identity, the one judged.
var errChanged = errors.New("changed while it was read")

// readJudged reads the configuration at path once the file and its directory
// are judged. A link is followed to the file it reaches; that file has to be
// regular, closed to writes by others, and owned by the plane's account or
// root, and so does the directory holding it, though a sticky directory may
// let others write, since only a file's owner may replace it. Each is judged on the
// descriptor the read goes through, so nothing swapped in after a check is
// read. A refusal names what was judged and why, never what the file holds.
func readJudged(path string) ([]byte, error) {
	if !files.PermissionBits {
		return nil, files.ErrNoPermissionBits
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	dir, name := filepath.Dir(resolved), filepath.Base(resolved)
	root, err := openJudgedDir(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	named, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	configStep(stepFileNamed)
	// A root follows a link at the name whatever the flags say, so the file
	// opened is held to be the entry named above, which a link never is.
	f, err := root.OpenFile(name, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(named, info) {
		return nil, fmt.Errorf("%s %w", resolved, errChanged)
	}
	if err := judge(info); err != nil {
		return nil, fmt.Errorf("%s: %w", resolved, err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxConfigBytes {
		return nil, fmt.Errorf("%w: limit %d", files.ErrTooLarge, maxConfigBytes)
	}
	return raw, nil
}

// openJudgedDir opens dir, judges it on the opened descriptor, and returns a
// root proven to be that same directory.
func openJudgedDir(dir string) (*os.Root, error) {
	d, err := files.OpenDir(dir)
	if err != nil {
		return nil, err
	}
	// Held open until the root is proven the same directory, so its identity
	// cannot pass to another directory in between.
	defer func() { _ = d.Close() }()
	judged, err := d.Stat()
	if err != nil {
		return nil, err
	}
	if err := judge(judged); err != nil {
		return nil, fmt.Errorf("its directory %s: %w", dir, err)
	}
	configStep(stepDirOpened)
	// The trailing "." fails on anything but a directory rather than wait on
	// a pipe.
	root, err := os.OpenRoot(dir + string(os.PathSeparator) + ".")
	if err != nil {
		return nil, err
	}
	rooted, err := root.Stat(".")
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	if !os.SameFile(judged, rooted) {
		return nil, errors.Join(fmt.Errorf("its directory %s %w", dir, errChanged), root.Close())
	}
	return root, nil
}

// judge refuses the configuration file, or its directory, unless it is the
// kind expected, closed to writes by others, and owned by the plane's
// account or root. A sticky directory may let others write.
func judge(info fs.FileInfo) error {
	mode := info.Mode()
	switch {
	case info.IsDir():
		if mode&fs.ModeSticky == 0 && mode.Perm()&writableByOthers != 0 {
			return fmt.Errorf("%w: mode %04o; others may not write it unless it is sticky", files.ErrMode, mode.Perm())
		}
	case !mode.IsRegular():
		return fmt.Errorf("%w: %s", files.ErrNotRegular, mode.Type())
	case mode.Perm()&writableByOthers != 0:
		return fmt.Errorf("%w: mode %04o; others may not write it", files.ErrMode, mode.Perm())
	}
	return checkConfigOwner(info)
}

// checkConfigOwner refuses a file or directory owned by an account other
// than the plane's or root: its owner may rewrite it whatever its mode says.
// Root is taken because a container mounts the configuration read-only as
// root's.
func checkConfigOwner(info fs.FileInfo) error {
	want := ownerWanted(info)
	err := files.CheckOwnedBy(info, want)
	if errors.Is(err, files.ErrOwner) && files.CheckOwnedBy(info, 0) == nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w; it has to be owned by the plane's account or root", err)
	}
	return nil
}
