package holdjournal

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// errExists is a name that was there already. Each caller translates it into
// the refusal its own operation documents, so no caller has to read a file
// system error to learn what happened.
const errExists Error = "holdjournal: the name exists already"

// writeSynced writes body to path, which no name points at yet, and forces it
// to disk. A file this fails on is removed: a temporary name nobody linked or
// renamed is not an entry, and the next open would only have to sweep it.
func writeSynced(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: a name this package composes under the directory checked at Open
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}

// tmpPath is a name no entry can take, under the journal's own directory.
func (j *Journal) tmpPath() string {
	return filepath.Join(j.dir, tmpPrefix+rand.Text()+tmpSuffix)
}

// create writes body under name, which must not be there: the link fails with
// EEXIST rather than replacing an entry that exists already. The file is
// forced to disk before the link and the directory after it, so a name that
// was reported written survives a power loss.
func (j *Journal) create(name string, body []byte) error {
	tmp := j.tmpPath()
	if err := writeSynced(tmp, body); err != nil {
		return err
	}
	err := os.Link(tmp, filepath.Join(j.dir, name))
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return errors.Join(errExists, os.Remove(tmp))
		}
		return errors.Join(err, os.Remove(tmp))
	}
	return errors.Join(os.Remove(tmp), syncDir(j.dir))
}

// replace puts body under name, over whatever is there. The rename is atomic,
// so a crash leaves the old state or the new one and never a torn file, and
// the directory is forced to disk before the write is reported durable.
func (j *Journal) replace(name string, body []byte) error {
	tmp := j.tmpPath()
	if err := writeSynced(tmp, body); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(j.dir, name)); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return syncDir(j.dir)
}

// remove unlinks one name and reports whether it was there. A name that is
// already gone is not a failure: forgetting a request the journal holds no
// entry for is what the caller does after every execution it closes.
func (j *Journal) remove(name string) (bool, error) {
	err := os.Remove(filepath.Join(j.dir, name))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, syncDir(j.dir)
}

// syncDir forces a directory entry to disk, so a name that was linked, renamed
// or unlinked before a power loss is in that state after it.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the directory checked at Open
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

// readBounded reads one file and refuses one larger than limit rather than
// holding what it claims to be. A file that is exactly limit bytes is read;
// one byte more is refused. Only a regular file is read at all: a named pipe
// or a device under an entry's name would hold this caller in open(2), and the
// journal's mutex with it, until a writer that need never come turns up.
func (j *Journal) readBounded(name string, limit int) ([]byte, error) {
	f, err := os.OpenFile(filepath.Join(j.dir, name), os.O_RDONLY|nonBlocking, 0) //nolint:gosec // G304: a name this package composes under the directory checked at Open
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrForeignFile, cause(name))
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("%w: over %d bytes", ErrEntryTooLarge, limit)
	}
	return raw, nil
}
