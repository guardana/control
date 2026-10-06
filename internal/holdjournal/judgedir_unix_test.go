//go:build unix

package holdjournal_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/holdjournal"
)

// dirOfMode makes a directory under a fresh one and gives it mode, set after
// the make so the umask cannot narrow it.
func dirOfMode(t *testing.T, mode fs.FileMode) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "holds")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestJudgeDirectoryRefusesWhatTheOpenRefuses: each directory a reader's open
// refuses for the directory itself is refused with the open's own sentinel,
// through a link as directly; a sound directory, and a link to one, pass.
func TestJudgeDirectoryRefusesWhatTheOpenRefuses(t *testing.T) {
	sound := dirOfMode(t, 0o700)
	group := dirOfMode(t, 0o770)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := func(target string) string {
		l := filepath.Join(t.TempDir(), "linked")
		if err := os.Symlink(target, l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	for _, c := range []struct {
		name, dir string
		want      error
	}{
		{"group-writable", group, holdjournal.ErrPermissions},
		{"world-writable", dirOfMode(t, 0o707), holdjournal.ErrPermissions},
		{"a link to a group-writable directory", link(group), holdjournal.ErrPermissions},
		{"a file", file, holdjournal.ErrNotAJournal},
		{"nothing at the name", filepath.Join(t.TempDir(), "missing"), holdjournal.ErrNotAJournal},
		{"no name", "", holdjournal.ErrNotAJournal},
	} {
		if j, err := holdjournal.OpenReadOnly(c.dir); !errors.Is(err, c.want) {
			if j != nil {
				_ = j.Close()
			}
			t.Fatalf("%s: OpenReadOnly = %v, want %v, so the case is not one the open refuses", c.name, err, c.want)
		}
		if err := holdjournal.JudgeDirectory(c.dir); !errors.Is(err, c.want) {
			t.Errorf("%s: JudgeDirectory = %v, want %v", c.name, err, c.want)
		}
	}
	for _, dir := range []string{sound, link(sound), dirOfMode(t, 0o750), dirOfMode(t, 0o755)} {
		if err := holdjournal.JudgeDirectory(dir); err != nil {
			t.Errorf("JudgeDirectory(%s) = %v, want a pass", dir, err)
		}
	}
}

// TestJudgeDirectoryRefusesAnotherAccountsDirectory: whoever owns the
// directory may plant or delete a hold, so a directory this account does not
// own, or one judged with no account named, is refused.
func TestJudgeDirectoryRefusesAnotherAccountsDirectory(t *testing.T) {
	dir := dirOfMode(t, 0o700)
	for _, uid := range []int{os.Geteuid() + 1, -1} {
		restore := holdjournal.SetOwnerWanted(func(fs.FileInfo) int { return uid })
		err := holdjournal.JudgeDirectory(dir)
		restore()
		if !errors.Is(err, holdjournal.ErrOwner) {
			t.Errorf("JudgeDirectory as uid %d = %v, want ErrOwner", uid, err)
		}
	}
	if err := holdjournal.JudgeDirectory(dir); err != nil {
		t.Errorf("JudgeDirectory as the owner = %v", err)
	}
}

// TestJudgeDirectoryTakesNoLockAndMakesNothing: a directory no plane has
// used is left as it was, with no marker, and a directory a plane holds is
// judged without waiting on its lock.
func TestJudgeDirectoryTakesNoLockAndMakesNothing(t *testing.T) {
	empty := dirOfMode(t, 0o700)
	if err := holdjournal.JudgeDirectory(empty); err != nil {
		t.Fatalf("JudgeDirectory of an empty directory = %v", err)
	}
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Errorf("the judged directory holds %v, %v; want nothing", entries, err)
	}
	held := dirOfMode(t, 0o700)
	openJournal(t, held)
	if second, err := holdjournal.Open(held); !errors.Is(err, holdjournal.ErrLocked) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("a second plane = %v, want ErrLocked, so the lock is not shown held", err)
	}
	if err := holdjournal.JudgeDirectory(held); err != nil {
		t.Errorf("JudgeDirectory of a held directory = %v, want a pass", err)
	}
}
