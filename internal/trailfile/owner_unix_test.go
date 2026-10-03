//go:build unix

package trailfile

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

// tornTail is a cut through the opening of a line, which Open removes from a
// file it takes and leaves in one it refuses.
const tornTail = `{"eventId":"e2`

// TestOpenRefusesADirectoryAnotherAccountOwns: whoever owns the directory can
// put any file at the trail's name, so neither a new file nor an existing one
// is opened in it.
func TestOpenRefusesADirectoryAnotherAccountOwns(t *testing.T) {
	wantOwner(t, func(fs.FileInfo) int { return os.Geteuid() + 1 })
	fresh := trailPath(t)
	if w, err := Open(fresh); !errors.Is(err, ErrOwner) || w != nil {
		t.Errorf("Open of a new file = %v, %v; want ErrOwner", w, err)
	}
	if _, err := os.Lstat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the refused Open left a file: %v", err)
	}
	existing := trailPath(t)
	writeFile(t, existing, firstLine+tornTail)
	if w, err := Open(existing); !errors.Is(err, ErrOwner) || w != nil {
		t.Errorf("Open of an existing file = %v, %v; want ErrOwner", w, err)
	}
	if got := contents(t, existing); got != firstLine+tornTail {
		t.Errorf("the refused Open changed the file to %q", got)
	}
	ownerWanted = func(fs.FileInfo) int { return os.Geteuid() }
	openWriter(t, existing)
}

// TestOpenRefusesAFileOfAnotherAccountInItsOwnDirectory: the directory being
// this account's does not make the file in it this account's, so the file's
// own owner is judged, and the refused file is left as it was found.
func TestOpenRefusesAFileOfAnotherAccountInItsOwnDirectory(t *testing.T) {
	path := trailPath(t)
	writeFile(t, path, firstLine+tornTail)
	wantOwner(t, func(info fs.FileInfo) int {
		if info.IsDir() {
			return os.Geteuid()
		}
		return os.Geteuid() + 1
	})
	if w, err := Open(path); !errors.Is(err, ErrOwner) || w != nil {
		t.Errorf("Open = %v, %v; want ErrOwner", w, err)
	}
	if got := contents(t, path); got != firstLine+tornTail {
		t.Errorf("the refused Open changed the file to %q", got)
	}
}

// wantOwner makes uid name the account each file and directory Open judges
// has to belong to, until the test ends.
func wantOwner(t *testing.T, uid func(fs.FileInfo) int) {
	t.Helper()
	saved := ownerWanted
	ownerWanted = uid
	t.Cleanup(func() { ownerWanted = saved })
}

// TestOpenRefusesAFileAnotherAccountOwns: that account can write the evidence
// whatever the mode says. Giving a file away needs root.
func TestOpenRefusesAFileAnotherAccountOwns(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving a file another owner needs root; this run is not root")
	}
	path := trailPath(t)
	writeFile(t, path, firstLine+tornTail)
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if w, err := Open(path); !errors.Is(err, ErrOwner) || w != nil {
		t.Errorf("Open = %v, %v; want ErrOwner", w, err)
	}
	if got := contents(t, path); got != firstLine+tornTail {
		t.Errorf("the refused Open changed the file to %q", got)
	}
}

// TestOpenRefusesALinkAtTheFile: a link that stays in the directory is one a
// handle on the directory would follow, so it is refused by its own entry,
// whether its target exists or not, and its target is never made or changed.
func TestOpenRefusesALinkAtTheFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	writeFile(t, target, firstLine+tornTail)
	for name, to := range map[string]string{"to a trail": "target", "to nothing": "absent"} {
		link := filepath.Join(dir, "link")
		if err := os.Symlink(to, link); err != nil {
			t.Fatal(err)
		}
		if w, err := Open(link); !errors.Is(err, ErrNotRegular) || w != nil {
			t.Errorf("a link %s: Open = %v, %v; want ErrNotRegular", name, w, err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
	if got := contents(t, target); got != firstLine+tornTail {
		t.Errorf("the link's target changed to %q", got)
	}
	if _, err := os.Lstat(filepath.Join(dir, "absent")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the dangling link's target was made: %v", err)
	}
}

// TestARefusedOpenKeepsNoDescriptor: an Open refused for the owner of the
// directory or of the file gives back the directory and the file it opened.
func TestARefusedOpenKeepsNoDescriptor(t *testing.T) {
	stopCollector(t)
	path := trailPath(t)
	writeFile(t, path, firstLine)
	before := openDescriptors(t)
	for _, dirsToo := range []bool{true, false} {
		wantOwner(t, func(info fs.FileInfo) int {
			if info.IsDir() && !dirsToo {
				return os.Geteuid()
			}
			return os.Geteuid() + 1
		})
		for range 16 {
			if w, err := Open(path); !errors.Is(err, ErrOwner) || w != nil {
				t.Fatalf("Open = %v, %v; want ErrOwner", w, err)
			}
		}
	}
	if after := openDescriptors(t); after != before {
		t.Errorf("%d descriptors open after the refused opens, %d before", after, before)
	}
}

// TestAnOpenedWriterKeepsOnlyItsFile: Open gives back the directory it opened
// the file through, and Close the file, so a collector reopened again and
// again does not run out of descriptors.
func TestAnOpenedWriterKeepsOnlyItsFile(t *testing.T) {
	stopCollector(t)
	path := trailPath(t)
	before := openDescriptors(t)
	for range 16 {
		w, err := Open(path)
		if err != nil {
			t.Fatalf("Open = %v", err)
		}
		if got := openDescriptors(t); got != before+1 {
			t.Errorf("%d descriptors open while a writer is, %d before it", got, before)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close = %v", err)
		}
	}
	if after := openDescriptors(t); after != before {
		t.Errorf("%d descriptors open after the opens and closes, %d before", after, before)
	}
}

// TestAnOpenRefusedByALinkKeepsNoDescriptor: an Open refused for the link at
// the file's name gives back the directory it opened.
func TestAnOpenRefusedByALinkKeepsNoDescriptor(t *testing.T) {
	stopCollector(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "target"), firstLine)
	link := filepath.Join(dir, "link")
	if err := os.Symlink("target", link); err != nil {
		t.Fatal(err)
	}
	before := openDescriptors(t)
	for range 16 {
		if w, err := Open(link); !errors.Is(err, ErrNotRegular) || w != nil {
			t.Fatalf("Open = %v, %v; want ErrNotRegular", w, err)
		}
	}
	if after := openDescriptors(t); after != before {
		t.Errorf("%d descriptors open after the refused opens, %d before", after, before)
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

// stopCollector keeps the collector off until t ends: os.File and os.Root
// close their descriptor in a finalizer, which would hide a leak from the
// count. The setting is the process's, so a test calling it is never parallel.
func stopCollector(t *testing.T) {
	t.Helper()
	percent := debug.SetGCPercent(-1)
	limit := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() {
		debug.SetMemoryLimit(limit)
		debug.SetGCPercent(percent)
	})
}
