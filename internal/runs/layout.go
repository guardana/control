package runs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/guardana/control/internal/files"
)

// A signed layout is a directory holding RecordsDir, the operators' signed
// records, which a plane reads and never writes, and StateDir, the plane's
// states, locks and carried closes, which no operator command writes.
const (
	RecordsDir = "records"
	StateDir   = "state"
)

const (
	// recordsDirMode and recordsFileMode let a plane of another account in
	// the records' group read them, and nobody but their owner write them.
	recordsDirMode  fs.FileMode = 0o750
	recordsFileMode fs.FileMode = 0o640
	stateDirMode    fs.FileMode = 0o700
)

// signedLayout reports whether dir is a signed layout: whether it holds
// records/. An unsigned directory holds regular files only.
func signedLayout(dir string) (bool, error) {
	info, err := os.Lstat(filepath.Join(dir, RecordsDir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("%w: %w", ErrNotRunsDir, err)
	case !info.IsDir():
		return false, fmt.Errorf("%w: %s is not a directory", ErrLayout, RecordsDir)
	}
	return true, nil
}

// judgeTop refuses a signed layout's directory that a group or the world may
// write, or that holds anything but the directories records/ and state/;
// state/ has to be there too where needState.
func judgeTop(dir string, needState bool) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotRunsDir, err)
	}
	if !info.IsDir() || info.Mode().Perm()&writableByOthers != 0 {
		return fmt.Errorf("%w: %s is not a directory only its owner may write", ErrPermissions, clip(dir))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotRunsDir, err)
	}
	state := false
	for _, e := range entries {
		switch {
		case !e.IsDir() || (e.Name() != RecordsDir && e.Name() != StateDir):
			return fmt.Errorf("%w: %q beside records/ and state/", ErrForeignFile, clip(e.Name()))
		case e.Name() == StateDir:
			state = true
		}
	}
	if needState && !state {
		return fmt.Errorf("%w: it holds no %s/ for the plane", ErrLayout, StateDir)
	}
	return nil
}

// recordsPath is the absolute, cleaned path of dir's records/, which a
// signed record names and a reader compares with its own.
func recordsPath(dir string) (string, error) {
	p, err := filepath.Abs(filepath.Join(dir, RecordsDir))
	if err != nil {
		return "", err
	}
	if err := checkRecordsPath(p); err != nil {
		return "", fmt.Errorf("the runs directory's path: %w", err)
	}
	return p, nil
}

// InitSignedAdmin opens dir as a signed layout for the operator. An empty
// directory becomes one first: it gains records/, for its owner to write and
// its group to read, with its marker, and state/, for its owner alone, which
// is handed to the plane's account before a plane opens it. A directory
// holding anything else, unsigned runs among them, is refused with
// ErrLayout.
func InitSignedAdmin(ctx context.Context, dir string) (*Admin, error) {
	signed, err := signedLayout(dir)
	if err != nil {
		return nil, err
	}
	if !signed {
		if err := makeSignedLayout(dir); err != nil {
			return nil, err
		}
	}
	return openSignedAdmin(ctx, dir, true)
}

func makeSignedLayout(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotRunsDir, err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("%w: %s holds files already; a signed layout starts in an empty directory", ErrLayout, clip(dir))
	}
	for _, sub := range []struct {
		name string
		mode fs.FileMode
	}{{RecordsDir, recordsDirMode}, {StateDir, stateDirMode}} {
		path := filepath.Join(dir, sub.name)
		if err := os.Mkdir(path, sub.mode); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := os.Chmod(path, sub.mode); err != nil {
			return err
		}
	}
	return files.SyncDir(dir)
}

// openSignedAdmin opens a signed layout's records/ for the operator, which
// is this user's; initialise makes an empty records/ a runs directory.
func openSignedAdmin(ctx context.Context, dir string, initialise bool) (*Admin, error) {
	if err := judgeTop(dir, false); err != nil {
		return nil, err
	}
	path, err := recordsPath(dir)
	if err != nil {
		return nil, err
	}
	d, err := openDir(filepath.Join(dir, RecordsDir), roleRecords, false)
	if err != nil {
		return nil, err
	}
	a := &Admin{d: d, records: path}
	if err := a.init(ctx, initialise); err != nil {
		return nil, errors.Join(err, d.close())
	}
	return a, nil
}

// OpenSignedPlane opens a signed layout for a plane under the signed
// authority, whose records count only once j took them; a nil j takes none.
// records/ may be another account's, and every record has to be that
// account's; state/ has to be this user's. Like OpenPlane it refuses a
// directory a group or the world may write and a file this package does not
// write, and it writes nothing: a state, a lock or a carried close is
// written when a call needs it. An unsigned directory is refused with
// ErrLayout.
func OpenSignedPlane(dir string, j Judge) (*Plane, error) {
	signed, err := signedLayout(dir)
	switch {
	case err != nil:
		return nil, err
	case !signed:
		return nil, fmt.Errorf("%w: %s holds unsigned runs; under the signed authority runs are opened with --key into a new directory", ErrLayout, clip(dir))
	}
	if err := judgeTop(dir, true); err != nil {
		return nil, err
	}
	path, err := recordsPath(dir)
	if err != nil {
		return nil, err
	}
	records, err := openDir(filepath.Join(dir, RecordsDir), roleRecords, true)
	if err != nil {
		return nil, err
	}
	if err := records.checkContents(); err != nil {
		return nil, errors.Join(err, records.close())
	}
	state, err := openDir(filepath.Join(dir, StateDir), roleState, false)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%s/: %w", StateDir, err), records.close())
	}
	if err := state.checkContents(); err != nil {
		return nil, errors.Join(err, state.close(), records.close())
	}
	return &Plane{d: records, st: state, judge: j, signed: true, records: path, counts: newSignedCounts()}, nil
}
