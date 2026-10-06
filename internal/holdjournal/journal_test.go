package holdjournal_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/holdjournal"
)

// TestASecondPlaneIsRefusedByTheLock: one process writes the journal, and what
// keeps the second one out is the directory's lock, not a file it could find
// missing. The refusal is asserted as the lock's own.
func TestASecondPlaneIsRefusedByTheLock(t *testing.T) {
	dir := newDir(t)
	first := openJournal(t, dir)

	second, err := holdjournal.Open(dir)
	if !errors.Is(err, holdjournal.ErrLocked) {
		t.Fatalf("a second plane over one directory: %v, want ErrLocked", err)
	}
	if second != nil {
		t.Fatal("a refused open handed back a journal")
	}
	// A reader takes no lock, so a command that only inspects a plane reads
	// the directory the plane is writing.
	reader, err := holdjournal.OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("reading a directory a plane holds: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("closing the reader: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing the first journal: %v", err)
	}
	again, err := holdjournal.Open(dir)
	if err != nil {
		t.Fatalf("opening the directory the first journal released: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
}

// TestADirectoryThatIsNotAJournalIsRefused: never read as an empty one, which
// would say that every hold was closed.
func TestADirectoryThatIsNotAJournalIsRefused(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"a directory holding somebody else's file": func(t *testing.T, dir string) {
			t.Helper()
			writeFile(t, filepath.Join(dir, "notes.txt"), []byte("not a journal"))
		},
		"a marker naming another kind": func(t *testing.T, dir string) {
			t.Helper()
			writeFile(t, filepath.Join(dir, "journal.meta"), []byte(`{"schema_version":"1.0","kind":"approvals"}`))
		},
		"a marker from a major nobody reads": func(t *testing.T, dir string) {
			t.Helper()
			writeFile(t, filepath.Join(dir, "journal.meta"), []byte(`{"schema_version":"2.0","kind":"hold_journal"}`))
		},
		"a marker that is not an object": func(t *testing.T, dir string) {
			t.Helper()
			writeFile(t, filepath.Join(dir, "journal.meta"), []byte("journal"))
		},
	}
	for name, prepare := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			prepare(t, dir)
			j, err := holdjournal.Open(dir)
			if !errors.Is(err, holdjournal.ErrNotAJournal) {
				t.Fatalf("opening %s: %v, want ErrNotAJournal", name, err)
			}
			if j != nil {
				t.Fatal("a refused open handed back a journal")
			}
			if _, err := holdjournal.OpenReadOnly(dir); !errors.Is(err, holdjournal.ErrNotAJournal) {
				t.Fatalf("reading %s: %v, want ErrNotAJournal", name, err)
			}
		})
	}
}

// TestAMarkerIsReadStrictly: the marker decides whether a directory is a
// journal, so it is held to the members it names exactly. encoding/json
// would fold a member's case, skip an unknown one and keep the last of two.
func TestAMarkerIsReadStrictly(t *testing.T) {
	cases := map[string]string{
		"an unknown member":       `{"schema_version":"1.0","kind":"hold_journal","owner":"anyone"}`,
		"members in capitals":     `{"Schema_Version":"1.0","KIND":"hold_journal"}`,
		"the kind given twice":    `{"schema_version":"1.0","kind":"approvals","kind":"hold_journal"}`,
		"the version given twice": `{"schema_version":"9.0","schema_version":"1.0","kind":"hold_journal"}`,
	}
	for name, marker := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			writeFile(t, filepath.Join(dir, "journal.meta"), []byte(marker))
			if _, err := holdjournal.Open(dir); !errors.Is(err, holdjournal.ErrNotAJournal) {
				t.Errorf("opening %s: %v, want ErrNotAJournal", name, err)
			}
			if _, err := holdjournal.OpenReadOnly(dir); !errors.Is(err, holdjournal.ErrNotAJournal) {
				t.Errorf("reading %s: %v, want ErrNotAJournal", name, err)
			}
		})
	}
	dir := newDir(t)
	writeFile(t, filepath.Join(dir, "journal.meta"), []byte(`{"schema_version":"1.0","kind":"hold_journal"}`+"\n"))
	j, err := holdjournal.Open(dir)
	if err != nil {
		t.Fatalf("opening the marker as the format spells it: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
}

// TestAForeignFileRefusesTheOpen: a journal is never opened over a directory
// holding a name it did not write, marker or not.
func TestAForeignFileRefusesTheOpen(t *testing.T) {
	dir := newDir(t)
	j := openJournal(t, dir)
	record(t, j, "req-1")
	if err := j.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
	writeFile(t, filepath.Join(dir, "somebody-elses-file"), []byte("."))
	if _, err := holdjournal.Open(dir); !errors.Is(err, holdjournal.ErrForeignFile) {
		t.Fatalf("opening a journal holding a foreign file: %v, want ErrForeignFile", err)
	}
	if _, err := holdjournal.OpenReadOnly(dir); !errors.Is(err, holdjournal.ErrForeignFile) {
		t.Fatalf("reading a journal holding a foreign file: %v, want ErrForeignFile", err)
	}
}

// TestOpenSweepsTheWritesNobodyPutInPlace: a temporary file a crash left is
// removed by the plane that opens the journal next, and by nothing that only
// reads it; it is never counted as an entry.
func TestOpenSweepsTheWritesNobodyPutInPlace(t *testing.T) {
	ctx := context.Background()
	dir := newDir(t)
	j := openJournal(t, dir)
	record(t, j, "req-1")
	if err := j.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
	leftover := filepath.Join(dir, "tmp.left-by-a-crash.part")
	writeFile(t, leftover, []byte("half an entry"))

	reader, err := holdjournal.OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("reading a journal with a leftover write: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("closing the reader: %v", err)
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("a reader removed the leftover write: %v", err)
	}

	plane := openJournal(t, dir, holdjournal.WithMaxEntries(2))
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the leftover write after the plane opened: %v, want it gone", err)
	}
	record(t, plane, "req-2")
	listing, err := plane.List(ctx, 8)
	if err != nil || len(listing.Held) != 2 || !listing.Complete {
		t.Fatalf("after the sweep the listing is %+v, %v", listing, err)
	}
}

// TestAnEmptyDirectoryIsAJournalOnlyOnceAPlaneHasMadeItOne: a reader refuses
// what no plane has written, so a report about a journal that does not exist
// is never a report of no lost holds.
func TestAnEmptyDirectoryIsAJournalOnlyOnceAPlaneHasMadeItOne(t *testing.T) {
	dir := newDir(t)
	if _, err := holdjournal.OpenReadOnly(dir); !errors.Is(err, holdjournal.ErrNotAJournal) {
		t.Fatalf("reading an empty directory: %v, want ErrNotAJournal", err)
	}
	j := openJournal(t, dir)
	if err := j.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
	reader, err := holdjournal.OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("reading the directory a plane made a journal: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("closing the reader: %v", err)
	}
}

// TestAGroupOrWorldWritableDirectoryIsRefused: whoever can write the journal
// can close a trail the plane never held.
func TestAGroupOrWorldWritableDirectoryIsRefused(t *testing.T) {
	for _, mode := range []os.FileMode{0o770, 0o707, 0o777, 0o722} {
		dir := newDir(t)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatalf("setting mode %04o: %v", mode, err)
		}
		if _, err := holdjournal.Open(dir); !errors.Is(err, holdjournal.ErrPermissions) {
			t.Fatalf("opening a directory in mode %04o: %v, want ErrPermissions", mode, err)
		}
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its execute bit; 0700 is owner-only
			t.Fatalf("setting mode 0700: %v", err)
		}
		if j, err := holdjournal.Open(dir); err != nil {
			t.Fatalf("opening the same directory in mode 0700: %v", err)
		} else if err := j.Close(); err != nil {
			t.Fatalf("closing the journal: %v", err)
		}
	}
}

// TestAForeignFileRefusesThePassRatherThanBeingSkipped: a name this package
// did not write appearing under the directory is not something to read past.
func TestAForeignFileRefusesThePassRatherThanBeingSkipped(t *testing.T) {
	dir := newDir(t)
	j := openJournal(t, dir)
	record(t, j, "req-1")
	writeFile(t, filepath.Join(dir, "somebody-elses-file"), []byte("."))

	listing, err := j.List(context.Background(), 8)
	if !errors.Is(err, holdjournal.ErrForeignFile) {
		t.Fatalf("listing a directory holding a foreign file: %v, want ErrForeignFile", err)
	}
	if listing.Complete || len(listing.Held) != 0 {
		t.Fatalf("a refused pass reported %+v", listing)
	}
}

// TestAReaderWritesNothing: every write through a read-only handle is refused
// by the compiler's other half, the handle itself.
func TestAReaderWritesNothing(t *testing.T) {
	ctx := context.Background()
	dir := newDir(t)
	plane := openJournal(t, dir)
	record(t, plane, "req-1")

	reader, err := holdjournal.OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("opening the reader: %v", err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Errorf("closing the reader: %v", err)
		}
	})
	if err := reader.Record(ctx, heldEntry("req-2")); !errors.Is(err, holdjournal.ErrReadOnly) {
		t.Fatalf("recording through a reader: %v, want ErrReadOnly", err)
	}
	if err := reader.Mark(ctx, "req-1", holdjournal.StateClosing); !errors.Is(err, holdjournal.ErrReadOnly) {
		t.Fatalf("flipping through a reader: %v, want ErrReadOnly", err)
	}
	if err := reader.Forget(ctx, "req-1"); !errors.Is(err, holdjournal.ErrReadOnly) {
		t.Fatalf("forgetting through a reader: %v, want ErrReadOnly", err)
	}
	listing, err := reader.List(ctx, 8)
	if err != nil {
		t.Fatalf("listing through a reader: %v", err)
	}
	if len(listing.Held) != 1 || listing.Held[0].IDs.RequestID != "req-1" {
		t.Fatalf("the reader listed %+v", listing)
	}
	if names := entryFiles(t, dir); len(names) != 1 {
		t.Fatalf("the reader left the directory holding %v", names)
	}
}

// TestAJournalNobodyOpenedAnswersNothing: the zero value and a closed handle
// both fail closed, so a wiring fault holds no call open on a journal that
// never existed.
func TestAJournalNobodyOpenedAnswersNothing(t *testing.T) {
	ctx := context.Background()
	var zero holdjournal.Journal
	closed := openJournal(t, newDir(t))
	if err := closed.Close(); err != nil {
		t.Fatalf("closing the journal: %v", err)
	}
	for name, j := range map[string]*holdjournal.Journal{"the zero value": &zero, "a closed handle": closed} {
		t.Run(name, func(t *testing.T) {
			if err := j.Record(ctx, heldEntry("req-1")); !errors.Is(err, holdjournal.ErrClosed) {
				t.Fatalf("recording on %s: %v, want ErrClosed", name, err)
			}
			if err := j.Mark(ctx, "req-1", holdjournal.StateClosing); !errors.Is(err, holdjournal.ErrClosed) {
				t.Fatalf("flipping on %s: %v, want ErrClosed", name, err)
			}
			if err := j.Forget(ctx, "req-1"); !errors.Is(err, holdjournal.ErrClosed) {
				t.Fatalf("forgetting on %s: %v, want ErrClosed", name, err)
			}
			if _, err := j.List(ctx, 8); !errors.Is(err, holdjournal.ErrClosed) {
				t.Fatalf("listing on %s: %v, want ErrClosed", name, err)
			}
			if err := j.Close(); !errors.Is(err, holdjournal.ErrClosed) {
				t.Fatalf("closing %s: %v, want ErrClosed", name, err)
			}
		})
	}
}

// TestACancelledContextWritesNothing: a caller that gave up is not a reason to
// leave an entry behind.
func TestACancelledContextWritesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	j := openJournal(t, newDir(t))
	if err := j.Record(ctx, heldEntry("req-1")); !errors.Is(err, context.Canceled) {
		t.Fatalf("recording under a cancelled context: %v, want context.Canceled", err)
	}
	if err := j.Mark(ctx, "req-1", holdjournal.StateClosing); !errors.Is(err, context.Canceled) {
		t.Fatalf("flipping under a cancelled context: %v, want context.Canceled", err)
	}
	if err := j.Forget(ctx, "req-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("forgetting under a cancelled context: %v, want context.Canceled", err)
	}
	if _, err := j.List(ctx, 8); !errors.Is(err, context.Canceled) {
		t.Fatalf("listing under a cancelled context: %v, want context.Canceled", err)
	}
	if names := entryFiles(t, j.Dir()); len(names) != 0 {
		t.Fatalf("a cancelled caller left %v behind", names)
	}
}

// TestAnOptionOutsideItsRangeIsRefused: a bound of zero would be a journal
// that holds nothing and says so as if the directory were empty.
func TestAnOptionOutsideItsRangeIsRefused(t *testing.T) {
	for _, opt := range []holdjournal.Option{
		holdjournal.WithMaxEntries(0),
		holdjournal.WithMaxEntries(-1),
		holdjournal.WithMaxEntryBytes(0),
		holdjournal.WithMaxEntryBytes(-1),
	} {
		if _, err := holdjournal.Open(newDir(t), opt); !errors.Is(err, holdjournal.ErrInvalidOption) {
			t.Fatalf("opening with an option outside its range: %v, want ErrInvalidOption", err)
		}
	}
}
