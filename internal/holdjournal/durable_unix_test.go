//go:build unix

package holdjournal_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/guardana/control/internal/holdjournal"
)

// TestEveryFileTheJournalWritesIsItsOwnersAlone: under a umask that narrows
// nothing, the marker, a recorded entry and a flipped one are each readable
// and writable by their owner and nobody else.
func TestEveryFileTheJournalWritesIsItsOwnersAlone(t *testing.T) {
	dir := newDir(t)
	old := syscall.Umask(0)
	j, err := holdjournal.Open(dir)
	if err == nil {
		err = errors.Join(
			j.Record(context.Background(), heldEntry("req-1")),
			j.Record(context.Background(), heldEntry("req-2")),
			j.Mark(context.Background(), "req-2", holdjournal.StateResuming),
			j.Close(),
		)
	}
	syscall.Umask(old)
	if err != nil {
		t.Fatal(err)
	}
	names := append(entryFiles(t, dir), "journal.meta")
	if len(names) != 3 {
		t.Fatalf("the directory holds %v, want two entries and the marker", names)
	}
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s: mode %04o, want 0600", name, got)
		}
	}
}

// TestARecordThatCannotBeMadeDurableIsNotFiled: the entry is linked, and then
// the directory cannot be opened to force it to disk. Record reports the
// failure, and the entry is gone with it, so a caller told the hold was not
// journalled never meets that hold again at the next start.
func TestARecordThatCannotBeMadeDurableIsNotFiled(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the superuser opens a directory whatever its mode")
	}
	dir := newDir(t)
	j := openJournal(t, dir)
	if err := os.Chmod(dir, 0o300); err != nil { //nolint:gosec // G302: write and search without read, so only the directory's own open fails
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: as newDir leaves it
	err := j.Record(context.Background(), heldEntry("req-1"))
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil { //nolint:gosec // G302: as newDir leaves it
		t.Fatal(chmodErr)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Record with a directory it cannot force to disk = %v, want fs.ErrPermission", err)
	}
	if names := entryFiles(t, dir); len(names) != 0 {
		t.Fatalf("a failed record left %v", names)
	}
	l, err := j.List(context.Background(), 10)
	if err != nil || len(l.Held)+l.Interrupted+l.Unreadable != 0 || !l.Complete {
		t.Fatalf("listing after a failed record: %+v, %v", l, err)
	}
	record(t, j, "req-1")
}
