//go:build unix

package findinglog

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
	info, err := os.Lstat(filepath.Join(dir, "findings.jsonl"))
	mustDo(t, err)
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 0 {
		t.Errorf("the log file is %v of %d bytes, want an empty regular file of mode 0600", info.Mode(), info.Size())
	}
}

// TestOpenRefusesADirectoryTheGroupOrOthersMayReach: one bit for the group
// or for others is enough, whether it reads, writes or only enters.
func TestOpenRefusesADirectoryTheGroupOrOthersMayReach(t *testing.T) {
	for _, mode := range []fs.FileMode{0o750, 0o705, 0o710, 0o701, 0o720, 0o702, 0o740, 0o704} {
		dir := logDir(t)
		mustDo(t, os.Chmod(dir, mode))
		if l, err := Open(dir); !errors.Is(err, ErrDirMode) || l != nil {
			t.Errorf("mode %04o: Open = %v, %v; want ErrDirMode", mode, l, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("mode %04o: the refused Open left a file: %v", mode, err)
		}
	}
}

func TestOpenRefusesALogFileTheGroupOrOthersMayReach(t *testing.T) {
	for _, mode := range []fs.FileMode{0o640, 0o620, 0o610, 0o604, 0o602, 0o601} {
		dir := logDir(t)
		path := filepath.Join(dir, FileName)
		writeFile(t, path, "")
		mustDo(t, os.Chmod(path, mode))
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

func TestOpenRefusesADirectoryOrALogFileOfAnotherAccount(t *testing.T) {
	for name, other := range map[string]func(fs.FileInfo) bool{
		"the directory": fs.FileInfo.IsDir,
		"the log file":  func(info fs.FileInfo) bool { return !info.IsDir() },
	} {
		dir := logDir(t)
		writeFile(t, filepath.Join(dir, FileName), "")
		wantOwner(t, func(info fs.FileInfo) int {
			if other(info) {
				return os.Geteuid() + 1
			}
			return os.Geteuid()
		})
		if l, err := Open(dir); !errors.Is(err, ErrOwner) || l != nil {
			t.Errorf("%s of another account: Open = %v, %v; want ErrOwner", name, l, err)
		}
	}
}

// TestOpenRefusesWhatIsNoDirectory: a link names an owner-only directory, and
// a missing directory is not made.
func TestOpenRefusesWhatIsNoDirectory(t *testing.T) {
	dir := logDir(t)
	link := filepath.Join(filepath.Dir(dir), "link")
	mustDo(t, os.Symlink(dir, link))
	file := filepath.Join(dir, "file")
	writeFile(t, file, "")
	missing := filepath.Join(dir, "missing")
	for _, path := range []string{link, link + "/", file, missing, ""} {
		if l, err := Open(path); err == nil || l != nil {
			t.Errorf("%q: Open = %v, %v; want a refusal", path, l, err)
		}
	}
	if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open made the missing directory: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, FileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an Open through the link made the log file: %v", err)
	}
}

func TestOpenRefusesWhatIsNoRegularLogFile(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, path string){
		"a link": func(t *testing.T, path string) {
			mustDo(t, os.Symlink(path+".real", path))
			writeFile(t, path+".real", "")
		},
		"a directory": func(t *testing.T, path string) { mustDo(t, os.Mkdir(path, 0o700)) },
	} {
		dir := logDir(t)
		plant(t, filepath.Join(dir, FileName))
		if l, err := Open(dir); !errors.Is(err, ErrNotRegular) || l != nil {
			t.Errorf("%s at the log file: Open = %v, %v; want ErrNotRegular", name, l, err)
		}
	}
}

// TestOpenRefusesALogFileWithASecondName: a hard link is a second name the
// log's own checks of its name would not see written through.
func TestOpenRefusesALogFileWithASecondName(t *testing.T) {
	dir := logDir(t)
	path := filepath.Join(dir, FileName)
	writeFile(t, path, "")
	mustDo(t, os.Link(path, filepath.Join(dir, "second")))
	if l, err := Open(dir); !errors.Is(err, ErrLinks) || l != nil {
		t.Errorf("Open = %v, %v; want ErrLinks", l, err)
	}
}

func TestASecondOpenIsRefusedUntilTheFirstCloses(t *testing.T) {
	dir := logDir(t)
	first := openLog(t, dir)
	if l, err := Open(dir); !errors.Is(err, ErrLocked) || l != nil {
		t.Errorf("a second Open = %v, %v; want ErrLocked", l, err)
	}
	mustDo(t, first.Close())
	openLog(t, dir)
}

// TestOpenRefusesALogLargerThanItsBound: both files are sparse and hold no
// newline, so the one at the bound passes it and is refused as damaged.
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
		if !errors.Is(err, c.want) || l != nil || (errors.Is(c.want, ErrDamaged) && errors.Is(err, ErrTooLarge)) {
			t.Errorf("%d bytes: Open = %v, %v; want %v", c.size, l, err, c.want)
		}
		if info, err := os.Stat(path); err != nil || info.Size() != c.size {
			t.Errorf("%d bytes: the refused Open changed the file: %v, %v", c.size, info, err)
		}
	}
}
