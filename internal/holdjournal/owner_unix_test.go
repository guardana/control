//go:build unix

package holdjournal_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/holdjournal"
)

// TestADirectoryAnotherAccountOwnsIsRefused: whoever owns the directory can
// loosen its mode or plant an entry, so a plane and a reader both refuse one
// this process's account does not own, and one whose owner is not named. A
// refused plane leaves the lock free for the next one.
func TestADirectoryAnotherAccountOwnsIsRefused(t *testing.T) {
	dir := newDir(t)
	if err := openJournal(t, dir).Close(); err != nil {
		t.Fatalf("closing the journal that made the directory: %v", err)
	}
	for owner, uid := range map[string]int{"another account": os.Geteuid() + 1, "no account named": -1} {
		restore := holdjournal.SetOwnerWanted(func(info fs.FileInfo) int {
			if info.IsDir() {
				return uid
			}
			return os.Geteuid()
		})
		for name, open := range map[string]func(string, ...holdjournal.Option) (*holdjournal.Journal, error){
			"a plane":  holdjournal.Open,
			"a reader": holdjournal.OpenReadOnly,
		} {
			j, err := open(dir)
			if !errors.Is(err, holdjournal.ErrOwner) || j != nil {
				t.Errorf("%s over a directory of %s: %v, %v; want ErrOwner", name, owner, j, err)
			}
		}
		restore()
	}
	j := openJournal(t, dir)
	if err := j.Close(); err != nil {
		t.Fatalf("the same directory under its own account: %v", err)
	}
}

// TestAFileOfAnotherAccountIsNotRead: only the plane writes the journal, so an
// entry or a marker of another account is one the plane never wrote. The
// directory stays this account's, so only the file's own owner refuses it.
func TestAFileOfAnotherAccountIsNotRead(t *testing.T) {
	dir := newDir(t)
	j := openJournal(t, dir)
	record(t, j, "req-given")
	record(t, j, "req-kept")
	given := filepath.Base(entryFileOf(t, dir, "req-given"))
	restore := holdjournal.SetOwnerWanted(ownedElsewhere(given))
	defer restore()
	listing, err := j.List(context.Background(), 8)
	if err != nil || listing.Complete || listing.Unreadable != 1 || len(listing.Held) != 1 ||
		listing.Held[0].IDs.RequestID != "req-kept" {
		t.Errorf("listing over an entry another account owns: %+v, %v; want it unreadable and the other held", listing, err)
	}
	if err := j.Mark(context.Background(), "req-given", holdjournal.StateClosing); !errors.Is(err, holdjournal.ErrOwner) {
		t.Errorf("flipping an entry another account owns: %v, want ErrOwner", err)
	}
	mark(t, j, "req-kept", holdjournal.StateClosing)
	if err := j.Close(); err != nil {
		t.Fatalf("closing the plane: %v", err)
	}
	restore()
	restore = holdjournal.SetOwnerWanted(ownedElsewhere("journal.meta"))
	for name, open := range map[string]func(string, ...holdjournal.Option) (*holdjournal.Journal, error){
		"a plane":  holdjournal.Open,
		"a reader": holdjournal.OpenReadOnly,
	} {
		if j, err := open(dir); !errors.Is(err, holdjournal.ErrOwner) || j != nil {
			t.Errorf("%s over a marker another account owns: %v, %v; want ErrOwner", name, j, err)
		}
	}
	restore()
	if err := openJournal(t, dir).Close(); err != nil {
		t.Errorf("a plane after the refused ones: %v; want the lock released", err)
	}
}

// TestADirectoryGivenAwayUnderAPlaneIsRefused: every call judges the
// directory's owner again, so one handed to another account while a plane
// runs takes no entry and closes no trail.
func TestADirectoryGivenAwayUnderAPlaneIsRefused(t *testing.T) {
	dir := newDir(t)
	j := openJournal(t, dir)
	record(t, j, "req-1")
	defer holdjournal.SetOwnerWanted(func(info fs.FileInfo) int {
		if info.IsDir() {
			return os.Geteuid() + 1
		}
		return os.Geteuid()
	})()
	if err := j.Record(context.Background(), heldEntry("req-2")); !errors.Is(err, holdjournal.ErrOwner) {
		t.Errorf("recording in a directory given away: %v, want ErrOwner", err)
	}
	if err := j.Mark(context.Background(), "req-1", holdjournal.StateClosing); !errors.Is(err, holdjournal.ErrOwner) {
		t.Errorf("flipping in a directory given away: %v, want ErrOwner", err)
	}
	if _, err := j.List(context.Background(), 8); !errors.Is(err, holdjournal.ErrOwner) {
		t.Errorf("listing a directory given away: %v, want ErrOwner", err)
	}
}

// ownedElsewhere wants another account for the file called name and this
// process's account for everything else.
func ownedElsewhere(name string) func(fs.FileInfo) int {
	return func(info fs.FileInfo) int {
		if info.Name() == name {
			return os.Geteuid() + 1
		}
		return os.Geteuid()
	}
}

// TestAnEntryAnotherAccountOwnsIsNotRead: only the plane writes the journal,
// so an entry of another account is one the plane never recorded. Giving a
// file away needs root.
func TestAnEntryAnotherAccountOwnsIsNotRead(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving a file another owner needs root; this run is not root")
	}
	dir := newDir(t)
	j := openJournal(t, dir)
	record(t, j, "req-given")
	if err := os.Chown(entryFileOf(t, dir, "req-given"), 65534, 65534); err != nil {
		t.Fatalf("giving the entry away: %v", err)
	}
	listing, err := j.List(context.Background(), 8)
	if err != nil || listing.Complete || len(listing.Held) != 0 || listing.Unreadable != 1 {
		t.Errorf("listing over an entry another account owns: %+v, %v; want it unreadable", listing, err)
	}
	if err := j.Mark(context.Background(), "req-given", holdjournal.StateClosing); !errors.Is(err, holdjournal.ErrOwner) {
		t.Errorf("flipping an entry another account owns: %v, want ErrOwner", err)
	}
}

// TestALinkAtAnEntryInsideTheDirectoryIsRefused: a link that stays inside the
// directory is one a handle on the directory would follow, so the entry is
// judged by its own directory entry before it is opened. A link to nothing is
// not an entry the journal lacks: something other than the journal put it
// there.
func TestALinkAtAnEntryInsideTheDirectoryIsRefused(t *testing.T) {
	for name, target := range map[string]func(path string) string{
		"to the entry moved aside": func(path string) string { return path + ".kept" },
		"to nothing":               func(path string) string { return path + ".absent" },
	} {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			j := openJournal(t, dir)
			record(t, j, "req-linked")
			path := entryFileOf(t, dir, "req-linked")
			if err := os.Rename(path, path+".kept"); err != nil {
				t.Fatalf("moving the entry: %v", err)
			}
			if err := os.Symlink(filepath.Base(target(path)), path); err != nil {
				t.Fatalf("linking the entry: %v", err)
			}
			err := j.Mark(context.Background(), "req-linked", holdjournal.StateClosing)
			if !errors.Is(err, holdjournal.ErrForeignFile) {
				t.Fatalf("flipping an entry that is a link: %v, want ErrForeignFile", err)
			}
			if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Errorf("the link was replaced: %v, %v", info, err)
			}
		})
	}
}

// TestADirectoryChangedUnderTheJournalIsRefused: the journal reaches its files
// through the directory it judged at open, and every call holds the name to
// that directory still, so a directory moved, replaced or loosened under a
// running plane takes no entry and closes no trail.
func TestADirectoryChangedUnderTheJournalIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		change func(t *testing.T, dir string)
		want   error
		// fresh is whether a directory stands at the name after the change.
		fresh bool
	}{
		"moved away": {change: func(t *testing.T, dir string) {
			t.Helper()
			mustRename(t, dir, dir+".moved")
		}, want: holdjournal.ErrDirectoryChanged},
		"replaced by another directory": {change: func(t *testing.T, dir string) {
			t.Helper()
			mustRename(t, dir, dir+".moved")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("making the new directory: %v", err)
			}
		}, want: holdjournal.ErrDirectoryChanged, fresh: true},
		"loosened": {change: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Chmod(dir, 0o770); err != nil { //nolint:gosec // G302: the mode under test
				t.Fatalf("loosening the directory: %v", err)
			}
		}, want: holdjournal.ErrPermissions},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "holds")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("making the directory: %v", err)
			}
			j := openJournal(t, dir)
			record(t, j, "req-1")
			c.change(t, dir)
			if err := j.Record(context.Background(), heldEntry("req-2")); !errors.Is(err, c.want) {
				t.Errorf("recording after the change: %v, want %v", err, c.want)
			}
			if err := j.Mark(context.Background(), "req-1", holdjournal.StateClosing); !errors.Is(err, c.want) {
				t.Errorf("flipping after the change: %v, want %v", err, c.want)
			}
			if err := j.Forget(context.Background(), "req-1"); !errors.Is(err, c.want) {
				t.Errorf("forgetting after the change: %v, want %v", err, c.want)
			}
			if _, err := j.List(context.Background(), 8); !errors.Is(err, c.want) {
				t.Errorf("listing after the change: %v, want %v", err, c.want)
			}
			if c.fresh {
				if names, err := os.ReadDir(dir); err != nil || len(names) != 0 {
					t.Errorf("the directory now at the name holds %v, %v; want nothing", names, err)
				}
			}
		})
	}
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatalf("renaming %q: %v", from, err)
	}
}

// TestARefusedOpenKeepsNoDescriptor: an open refused for a held lock, for a
// directory of another account or for a marker of another account gives back
// every descriptor it took, so a plane that keeps retrying does not run out.
func TestARefusedOpenKeepsNoDescriptor(t *testing.T) {
	held := newDir(t)
	openJournal(t, held)
	given := newDir(t)
	if err := openJournal(t, given).Close(); err != nil {
		t.Fatalf("closing the journal that made the directory: %v", err)
	}
	before := openDescriptors(t)
	for range 16 {
		if j, err := holdjournal.Open(held); !errors.Is(err, holdjournal.ErrLocked) || j != nil {
			t.Fatalf("a second plane: %v, %v; want ErrLocked", j, err)
		}
	}
	for _, wanted := range []func(fs.FileInfo) int{
		func(info fs.FileInfo) int {
			if info.IsDir() {
				return os.Geteuid() + 1
			}
			return os.Geteuid()
		},
		ownedElsewhere("journal.meta"),
	} {
		restore := holdjournal.SetOwnerWanted(wanted)
		for range 16 {
			for _, open := range []func(string, ...holdjournal.Option) (*holdjournal.Journal, error){holdjournal.Open, holdjournal.OpenReadOnly} {
				if j, err := open(given); !errors.Is(err, holdjournal.ErrOwner) || j != nil {
					t.Fatalf("an open of a journal of another account: %v, %v; want ErrOwner", j, err)
				}
			}
		}
		restore()
	}
	if after := openDescriptors(t); after != before {
		t.Errorf("%d descriptors open after the refused opens, %d before", after, before)
	}
}

// TestAClosedJournalKeepsNoDescriptor: Close gives back the directory and the
// lock, for a plane and for a reader, so one opened again and again does not
// run out of descriptors.
func TestAClosedJournalKeepsNoDescriptor(t *testing.T) {
	dir := newDir(t)
	before := openDescriptors(t)
	for range 16 {
		for _, open := range []func(string, ...holdjournal.Option) (*holdjournal.Journal, error){holdjournal.Open, holdjournal.OpenReadOnly} {
			j, err := open(dir)
			if err != nil {
				t.Fatalf("opening the journal: %v", err)
			}
			if err := j.Close(); err != nil {
				t.Fatalf("closing the journal: %v", err)
			}
		}
	}
	if after := openDescriptors(t); after != before {
		t.Errorf("%d descriptors open after the opens and closes, %d before", after, before)
	}
}

func openDescriptors(t *testing.T) int {
	t.Helper()
	fds, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatalf("listing this process's descriptors: %v", err)
	}
	return len(fds)
}
