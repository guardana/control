//go:build unix

package observelog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenTakesAnOwnerOnlyDirectoryAndCreatesTheFileOwnerOnly(t *testing.T) {
	dir := logDir(t)
	openLog(t, dir)
	info, err := os.Lstat(filepath.Join(dir, "observations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Errorf("the log file is %v, want a regular file of mode 0600", info.Mode())
	}
}

// TestOpenRefusesADirectoryTheGroupOrOthersMayReach: one bit for the group
// or for others is enough, whether it reads, writes or only enters.
func TestOpenRefusesADirectoryTheGroupOrOthersMayReach(t *testing.T) {
	for _, mode := range []fs.FileMode{0o750, 0o705, 0o711, 0o701, 0o710, 0o720, 0o702, 0o704, 0o740} {
		dir := logDir(t)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if l, err := Open(dir); !errors.Is(err, ErrDirMode) || l != nil {
			t.Errorf("mode %04o: Open = %v, %v; want ErrDirMode", mode, l, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("mode %04o: the refused Open left a file: %v", mode, err)
		}
	}
}

func TestOpenRefusesALogFileTheGroupOrOthersMayRead(t *testing.T) {
	for _, mode := range []fs.FileMode{0o640, 0o604, 0o620, 0o602} {
		dir := logDir(t)
		path := filepath.Join(dir, FileName)
		writeFile(t, path, "")
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if l, err := Open(dir); !errors.Is(err, ErrFileMode) || l != nil {
			t.Errorf("mode %04o: Open = %v, %v; want ErrFileMode", mode, l, err)
		}
	}
}

func wantOwner(t *testing.T, f func(fs.FileInfo) int) {
	t.Helper()
	ownerWanted = f
	t.Cleanup(func() { ownerWanted = func(fs.FileInfo) int { return os.Geteuid() } })
}

func TestOpenRefusesADirectoryAnotherAccountOwns(t *testing.T) {
	dir := logDir(t)
	wantOwner(t, func(fs.FileInfo) int { return os.Geteuid() + 1 })
	if l, err := Open(dir); !errors.Is(err, ErrOwner) || l != nil {
		t.Errorf("Open = %v, %v; want ErrOwner", l, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refused Open left a file: %v", err)
	}
}

func TestOpenRefusesALogFileOfAnotherAccount(t *testing.T) {
	dir := logDir(t)
	writeFile(t, filepath.Join(dir, FileName), "")
	wantOwner(t, func(info fs.FileInfo) int {
		if info.IsDir() {
			return os.Geteuid()
		}
		return os.Geteuid() + 1
	})
	if l, err := Open(dir); !errors.Is(err, ErrOwner) || l != nil {
		t.Errorf("Open = %v, %v; want ErrOwner", l, err)
	}
}

// TestOpenRefusesALinkAtTheDirectory: the link names an owner-only directory
// that would pass every other check, so only the link refuses it.
func TestOpenRefusesALinkAtTheDirectory(t *testing.T) {
	target := logDir(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, link + "/"} {
		if l, err := Open(path); err == nil || l != nil {
			t.Errorf("Open(%q) = %v, %v; want a refusal", path, l, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(target, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refused Open left a file in the link's target: %v", err)
	}
	openLog(t, target)
}

func TestOpenRefusesALinkAtTheLogFile(t *testing.T) {
	dir := logDir(t)
	target := filepath.Join(dir, "elsewhere.jsonl")
	writeFile(t, target, "")
	if err := os.Symlink("elsewhere.jsonl", filepath.Join(dir, FileName)); err != nil {
		t.Fatal(err)
	}
	if l, err := Open(dir); !errors.Is(err, ErrNotRegular) || l != nil {
		t.Errorf("Open = %v, %v; want ErrNotRegular", l, err)
	}
}

func TestOpenRefusesADirectoryAtTheLogFile(t *testing.T) {
	dir := logDir(t)
	if err := os.Mkdir(filepath.Join(dir, FileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if l, err := Open(dir); !errors.Is(err, ErrNotRegular) || l != nil {
		t.Errorf("Open = %v, %v; want ErrNotRegular", l, err)
	}
}

func TestOpenRefusesAPathThatIsNoDirectory(t *testing.T) {
	dir := logDir(t)
	file := filepath.Join(dir, "plain")
	writeFile(t, file, "")
	for _, path := range []string{"", file, filepath.Join(dir, "missing")} {
		if l, err := Open(path); err == nil || l != nil {
			t.Errorf("Open(%q) = %v, %v; want a refusal", path, l, err)
		}
	}
}

func TestASecondOpenIsRefusedUntilTheFirstCloses(t *testing.T) {
	dir := logDir(t)
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l, err := Open(dir); !errors.Is(err, ErrLocked) || l != nil {
		t.Errorf("a second Open = %v, %v; want ErrLocked", l, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Errorf("a second Close = %v", err)
	}
	if _, err := first.Write(nil, report("2026-10-04T10:05:00Z")); !errors.Is(err, ErrClosed) {
		t.Errorf("Write after Close = %v; want ErrClosed", err)
	}
	openLog(t, dir)
}

// TestOpenRefusesALogFileWithASecondName: a hard link is a second name the
// writer's checks of the first one do not watch.
func TestOpenRefusesALogFileWithASecondName(t *testing.T) {
	dir := logDir(t)
	mustDo(t, openLogAndClose(dir))
	mustDo(t, os.Link(filepath.Join(dir, FileName), filepath.Join(dir, "second")))
	l, err := Open(dir)
	if !errors.Is(err, ErrLinks) || l != nil {
		t.Errorf("Open = %v, %v; want ErrLinks", l, err)
	}
	if l != nil {
		mustDo(t, l.Close())
	}
	mustDo(t, os.Remove(filepath.Join(dir, "second")))
	openLog(t, dir)
}

func TestALinkMadeUnderTheWriterRefusesEveryLaterWrite(t *testing.T) {
	dir := logDir(t)
	l := openLog(t, dir)
	mustDo(t, os.Link(filepath.Join(dir, FileName), filepath.Join(dir, "second")))
	for range 2 {
		if _, err := l.Write(nil, report("2026-10-04T10:05:00Z")); !errors.Is(err, ErrChanged) || !errors.Is(err, ErrLinks) {
			t.Errorf("Write = %v, want ErrChanged for ErrLinks", err)
		}
	}
	if b := contents(t, filepath.Join(dir, FileName)); len(b) != 0 {
		t.Errorf("the refused writes left %q", b)
	}
}

// TestOpenRefusesALogLargerThanItsBound: both files are sparse and hold no
// newline, so the one at the bound is refused as damaged, after the bound
// let it through.
func TestOpenRefusesALogLargerThanItsBound(t *testing.T) {
	for _, c := range []struct {
		size int64
		want error
	}{{1 << 30, ErrDamaged}, {1<<30 + 1, ErrTooLarge}} {
		dir := logDir(t)
		path := filepath.Join(dir, FileName)
		writeFile(t, path, "")
		mustDo(t, os.Truncate(path, c.size))
		l, err := Open(dir)
		if !errors.Is(err, c.want) || l != nil {
			t.Errorf("%d bytes: Open = %v, %v; want %v", c.size, l, err, c.want)
		}
		if errors.Is(c.want, ErrDamaged) && errors.Is(err, ErrTooLarge) {
			t.Errorf("%d bytes: refused as too large", c.size)
		}
	}
	if MaxLogBytes != 1<<30 {
		t.Errorf("MaxLogBytes = %d, want 1 GiB", MaxLogBytes)
	}
}

func openLogAndClose(dir string) error {
	l, err := Open(dir)
	if err != nil {
		return err
	}
	return l.Close()
}
