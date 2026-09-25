package approvals

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// swapDirs makes the directories under root: the one a plane opens, the
// one put at its name, and the name the first is moved to.
func swapDirs(t *testing.T) (dir, away, other string) {
	t.Helper()
	root := t.TempDir()
	dir, away, other = filepath.Join(root, "approvals"), filepath.Join(root, "away"), filepath.Join(root, "other")
	for _, d := range []string{dir, other} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir, away, other
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Error(err)
	}
}

// TestAPlaneLocksTheDirectoryItJudged: another directory stands at the name
// while the plane takes its lock, and the judged one is back by the time the
// name is judged again. The lock is on the judged directory all the same, so
// a second plane over it is refused and the directory that stood at the name
// is left free.
func TestAPlaneLocksTheDirectoryItJudged(t *testing.T) {
	dir, away, other := swapDirs(t)
	p, err := OpenPlane(dir, withSteps(func(step string) {
		switch step {
		case stepOpened:
			rename(t, dir, away)
			rename(t, other, dir)
		case stepLocked:
			rename(t, dir, other)
			rename(t, away, dir)
		}
	}))
	if err != nil {
		t.Fatalf("OpenPlane with the judged directory back at its name: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if q, err := OpenPlane(dir); !errors.Is(err, ErrLocked) {
		if q != nil {
			_ = q.Close()
		}
		t.Errorf("a second plane over the judged directory = %v, want ErrLocked", err)
	}
	q, err := OpenPlane(other)
	if err != nil {
		t.Fatalf("a plane over the directory that stood at the name: %v", err)
	}
	_ = q.Close()
}

// TestAPlaneRefusesANameThatNamesAnotherDirectory: the same swap, never put
// back. The plane holds the directory it judged, which the name no longer
// names, so the open is refused.
func TestAPlaneRefusesANameThatNamesAnotherDirectory(t *testing.T) {
	dir, away, other := swapDirs(t)
	p, err := OpenPlane(dir, withSteps(func(step string) {
		if step == stepOpened {
			rename(t, dir, away)
			rename(t, other, dir)
		}
	}))
	if !errors.Is(err, ErrDirectoryChanged) {
		t.Errorf("OpenPlane once another directory stands at its name = %v, want ErrDirectoryChanged", err)
	}
	if p != nil {
		_ = p.Close()
	}
}

// TestADirectoryIsJudgedByItsIdentityItsOwnerAndItsMode: each case differs
// from the directory as it was opened in one thing, and only a group or world
// write bit, another device or inode, or another owner refuses.
func TestADirectoryIsJudgedByItsIdentityItsOwnerAndItsMode(t *testing.T) {
	opened := dirID{dev: 7, ino: 42, uid: 501, perm: 0o700}
	for _, c := range []struct {
		name    string
		now     dirID
		refused bool
		perm    bool
	}{
		{"as opened", opened, false, false},
		{"readable by the group and the world", dirID{dev: 7, ino: 42, uid: 501, perm: 0o755}, false, false},
		{"narrowed", dirID{dev: 7, ino: 42, uid: 501, perm: 0o500}, false, false},
		{"another device", dirID{dev: 8, ino: 42, uid: 501, perm: 0o700}, true, false},
		{"another inode", dirID{dev: 7, ino: 43, uid: 501, perm: 0o700}, true, false},
		{"another owner", dirID{dev: 7, ino: 42, uid: 0, perm: 0o700}, true, false},
		{"writable by the group", dirID{dev: 7, ino: 42, uid: 501, perm: 0o720}, true, true},
		{"writable by the world", dirID{dev: 7, ino: 42, uid: 501, perm: 0o702}, true, true},
	} {
		err := opened.still(c.now)
		if got := errors.Is(err, ErrDirectoryChanged); got != c.refused {
			t.Errorf("%s: %v, want refused %v", c.name, err, c.refused)
		}
		if got := errors.Is(err, ErrPermissions); got != c.perm {
			t.Errorf("%s: %v, want ErrPermissions %v", c.name, err, c.perm)
		}
	}
}
