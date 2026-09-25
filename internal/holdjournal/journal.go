package holdjournal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// The defaults a journal is opened with. They bound what the directory can
// make a start cost: the entries one pass reads, and the bytes one entry
// takes.
const (
	// DefaultMaxEntries bounds the entries the directory may hold.
	DefaultMaxEntries = 1024
	// DefaultMaxEntryBytes bounds one entry's body. An entry this build
	// writes is well under a kilobyte.
	DefaultMaxEntryBytes = 16 << 10
)

type options struct {
	maxEntries    int
	maxEntryBytes int
}

// Option configures a journal at Open.
type Option func(*options) error

// WithMaxEntries bounds the entries the directory may hold. A hold past it is
// refused, and a listing past it reports itself incomplete rather than short.
func WithMaxEntries(n int) Option {
	return func(o *options) error {
		if n <= 0 {
			return fmt.Errorf("%w: max entries %d is not positive", ErrInvalidOption, n)
		}
		o.maxEntries = n
		return nil
	}
}

// WithMaxEntryBytes bounds one entry's body, on the way in and on the way out.
func WithMaxEntryBytes(n int) Option {
	return func(o *options) error {
		if n <= 0 {
			return fmt.Errorf("%w: max entry bytes %d is not positive", ErrInvalidOption, n)
		}
		o.maxEntryBytes = n
		return nil
	}
}

// Journal is one directory of hold entries, opened once. Its zero value is
// open on nothing: every method on it fails closed, which is what a composite
// literal from another package gets.
type Journal struct {
	mu       sync.Mutex
	dir      string
	opts     options
	open     bool
	writable bool
	unlock   func() error
	// entries is how many entry files the directory holds. Only this process
	// writes them, and it does so under the directory's exclusive lock, so a
	// count kept here needs no directory read per hold.
	entries int
}

// journalMarker says the directory is a hold journal.
type journalMarker struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind"`
}

const markerKind = "hold_journal"

// Open takes the directory's exclusive lock and returns the journal the plane
// writes. The lock is held until Close, so a second plane over one directory
// is refused with ErrLocked rather than allowed to write entries the first one
// will overwrite. Where the platform has no file lock the journal refuses to
// open: a second locking path that nothing here builds or tests is not
// something a durable record is trusted to.
func Open(dir string, opts ...Option) (*Journal, error) {
	return openJournal(dir, true, opts)
}

// OpenReadOnly returns a journal that reads and refuses every write with
// ErrReadOnly. It takes no lock, so a command that only inspects a plane can
// read the directory while that plane serves; each entry is written by an
// atomic rename, so every entry a pass reads is one a plane wrote whole,
// though a pass taken while the plane writes is a listing of one moment and
// not a transaction.
func OpenReadOnly(dir string, opts ...Option) (*Journal, error) {
	return openJournal(dir, false, opts)
}

func openJournal(dir string, writable bool, opts []Option) (*Journal, error) {
	o := options{maxEntries: DefaultMaxEntries, maxEntryBytes: DefaultMaxEntryBytes}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, err
		}
	}
	if err := checkDir(dir); err != nil {
		return nil, err
	}
	j := &Journal{dir: dir, opts: o, writable: writable, unlock: func() error { return nil }}
	if writable {
		unlock, err := lockDir(dir)
		if err != nil {
			return nil, err
		}
		j.unlock = unlock
	}
	if err := j.init(); err != nil {
		return nil, errors.Join(err, j.unlock())
	}
	j.open = true
	return j, nil
}

// checkDir refuses a path that is not a directory this journal may own. A
// group or the world being able to write it would make every hold theirs, and
// a forged entry closes a trail the plane never held.
func checkDir(dir string) error {
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %w", ErrNotAJournal, err)
	case !info.IsDir():
		return fmt.Errorf("%w: %q is not a directory", ErrNotAJournal, dir)
	case info.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("%w: mode %04o", ErrPermissions, info.Mode().Perm())
	}
	return nil
}

// init reads the marker, writing one where the plane opens an empty directory,
// and refuses a directory holding anything this package did not write. A
// directory that is not a journal is never read as an empty one: an empty
// journal says every hold was closed.
func (j *Journal) init() error {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotAJournal, err)
	}
	marker, temps, held, foreign := sortEntries(entries)
	if err := j.marker(marker, len(entries) == len(temps)); err != nil {
		return err
	}
	if foreign != "" {
		return fmt.Errorf("%w: %q", ErrForeignFile, cause(foreign))
	}
	j.entries = held
	if !j.writable {
		return nil
	}
	return j.sweep(temps)
}

// sortEntries says what a directory listing holds: whether the marker is
// there, which files are writes nobody put in place, how many entries there
// are, and the first name this package did not write.
func sortEntries(entries []os.DirEntry) (marker bool, temps []string, held int, foreign string) {
	for _, e := range entries {
		switch classifyEntry(e) {
		case kindMarker:
			marker = true
		case kindTemp:
			temps = append(temps, e.Name())
		case kindEntry:
			held++
		case kindForeign:
			if foreign == "" {
				foreign = e.Name()
			}
		}
	}
	return marker, temps, held, foreign
}

// marker reads the directory's marker, or writes one where the plane opens an
// empty directory. Anything else is not a journal.
func (j *Journal) marker(present, empty bool) error {
	switch {
	case present:
		return j.readMarker()
	case j.writable && empty:
		return j.writeMarker()
	}
	return fmt.Errorf("%w: %q holds no %s", ErrNotAJournal, j.dir, markerFile)
}

func (j *Journal) readMarker() error {
	raw, err := j.readBounded(markerFile, 4<<10)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotAJournal, err)
	}
	var m journalMarker
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("%w: %s", ErrNotAJournal, cause(err.Error()))
	}
	if m.Kind != markerKind {
		return fmt.Errorf("%w: the marker names kind %q", ErrNotAJournal, cause(m.Kind))
	}
	if err := checkSchemaVersion(m.SchemaVersion); err != nil {
		return fmt.Errorf("%w: the marker: %w", ErrNotAJournal, err)
	}
	return nil
}

func (j *Journal) writeMarker() error {
	raw, err := json.Marshal(journalMarker{SchemaVersion: SchemaVersion, Kind: markerKind})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotAJournal, err)
	}
	if err := j.create(markerFile, append(raw, '\n')); err != nil {
		return fmt.Errorf("%w: %w", ErrNotAJournal, err)
	}
	return nil
}

// sweep removes the writes nobody put in place. Only a plane sweeps, and only
// under its lock, so no live writer's file is taken away from it.
func (j *Journal) sweep(temps []string) error {
	for _, name := range temps {
		if err := os.Remove(filepath.Join(j.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %w", ErrNotAJournal, err)
		}
	}
	return nil
}

// Dir is the directory this journal reads and writes, which a command that
// reports on a plane's durable state names to an operator.
func (j *Journal) Dir() string {
	if j == nil {
		return ""
	}
	return j.dir
}

// usable reports whether the handle may act at all.
func (j *Journal) usable() error {
	if j == nil || !j.open {
		return ErrClosed
	}
	return nil
}

// writable reports whether the handle may write.
func (j *Journal) mayWrite() error {
	if err := j.usable(); err != nil {
		return err
	}
	if !j.writable {
		return ErrReadOnly
	}
	return nil
}

// Close releases the lock once. A journal closed twice says so rather than
// releasing a lock it no longer holds.
func (j *Journal) Close() error {
	if j == nil {
		return ErrClosed
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.open {
		return ErrClosed
	}
	j.open = false
	return j.unlock()
}
