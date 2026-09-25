package pause

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/guardana/control/internal/files"
)

// fileMode is the mode every write leaves the pause file in: whoever may
// write it may lift every pause.
const fileMode fs.FileMode = 0o600

// Init writes a pause file that pauses nothing at path, and refuses with
// ErrExists when the name is taken, by a file or by a link.
func Init(ctx context.Context, path string) error {
	return locked(ctx, path, func(root *os.Root) error {
		body, err := Marshal(Document{})
		if err != nil {
			return err
		}
		if err := files.CreateNoReplaceIn(root, filepath.Base(path), body, fileMode); err != nil {
			if errors.Is(err, files.ErrExists) {
				return fmt.Errorf("%w: %s", ErrExists, path)
			}
			return err
		}
		return nil
	})
}

// Add adds e to the pause file at path, refusing an entry Entry.Check
// refuses, an id the file holds already with ErrDuplicateID, and an entry
// past MaxEntries.
func Add(ctx context.Context, path string, e Entry) error {
	return update(ctx, path, func(d Document) (Document, error) {
		if slices.ContainsFunc(d.Entries, func(have Entry) bool { return have.ID == e.ID }) {
			return d, fmt.Errorf("%w: %q", ErrDuplicateID, e.ID)
		}
		d.Entries = append(d.Entries, e)
		return d, nil
	})
}

// Remove removes the entry under id from the pause file at path, refusing an
// id it does not hold with ErrNoEntry. Removing the last entry writes a
// document with none; nothing deletes the file, since a missing file blocks
// every call.
func Remove(ctx context.Context, path, id string) error {
	return update(ctx, path, func(d Document) (Document, error) {
		i := slices.IndexFunc(d.Entries, func(have Entry) bool { return have.ID == id })
		if i < 0 {
			return d, fmt.Errorf("%w: %q", ErrNoEntry, id)
		}
		d.Entries = slices.Delete(d.Entries, i, i+1)
		return d, nil
	})
}

// List reads the pause file at path with every check a plane's read makes,
// and takes no lock: a write replaces the file whole.
func List(path string) (Document, error) {
	return load(path)
}

// update reads the pause file under the lock, with every check a plane's
// read makes, and writes back what change makes of it. A file the plane
// would refuse is refused here too, rather than rewritten into one it reads.
func update(ctx context.Context, path string, change func(Document) (Document, error)) error {
	return locked(ctx, path, func(root *os.Root) error {
		doc, err := loadIn(root, path)
		if err != nil {
			return err
		}
		next, err := change(Document{Entries: slices.Clone(doc.Entries)})
		if err != nil {
			return err
		}
		body, err := Marshal(next)
		if err != nil {
			return err
		}
		return files.ReplaceIn(root, filepath.Base(path), body, fileMode)
	})
}

// locked runs write through the pause file's directory, as openDir judged
// and opened it, under the exclusive lock on that directory: a directory the
// group or others may write, or another account than this process's
// effective user owns, is refused, since a plane running as that user would
// refuse the file written there.
func locked(ctx context.Context, path string, write func(root *os.Root) error) error {
	if !permissionBits {
		return ErrNoPermissionBits
	}
	root, err := openDir(filepath.Dir(path))
	if err != nil {
		return err
	}
	release, err := lockDir(ctx, root)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	return errors.Join(write(root), release(), root.Close())
}
