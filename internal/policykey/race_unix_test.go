//go:build unix

package policykey

import (
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Someone who can write the parent of --out swaps the directory keygen made
// for a link to a directory of theirs. Each test makes the swap at one step
// through a hook, standing in for a race won there, and holds that no key and
// no mode change reaches the other directory.

// swapTree is the parent keygen writes under and the other account's
// directory, mode 0755 so a chmod through the link would show.
type swapTree struct{ dir, other, aside string }

func newSwapTree(t *testing.T) swapTree {
	t.Helper()
	parent := t.TempDir()
	other := filepath.Join(t.TempDir(), "theirs")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(other, 0o755); err != nil { //nolint:gosec // G302: a directory another account left open, as the attack needs
		t.Fatal(err)
	}
	return swapTree{dir: filepath.Join(parent, "keys"), other: other, aside: filepath.Join(parent, "moved")}
}

// swapByRemoval replaces the empty directory at dir with a link to other.
func (s swapTree) swapByRemoval(t *testing.T) {
	t.Helper()
	if err := os.Remove(s.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(s.other, s.dir); err != nil {
		t.Fatal(err)
	}
}

// swapByRename moves the directory at dir aside, where it keeps what a handle
// to it writes, and links dir to other.
func (s swapTree) swapByRename(t *testing.T) {
	t.Helper()
	if err := os.Rename(s.dir, s.aside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(s.other, s.dir); err != nil {
		t.Fatal(err)
	}
}

// untouched fails unless other holds nothing and keeps its mode.
func (s swapTree) untouched(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(s.other)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the other directory now holds %d entries, %s among them", len(entries), entries[0].Name())
	}
	info, err := os.Stat(s.other)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("the other directory's mode is %v (%v), want 0755", info.Mode().Perm(), err)
	}
}

func swapKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = 5
	}
	return ed25519.NewKeyFromSeed(seed)
}

// noCreate fails the test if any file is created at all.
func noCreate(t *testing.T) func(*os.Root, string, []byte, fs.FileMode) error {
	return func(*os.Root, string, []byte, fs.FileMode) error {
		t.Error("a file was created after the path stopped naming the directory made")
		return errors.New("refused by the test")
	}
}

func TestWriteKeyPairRefusesALinkSwappedInBeforeItsMode(t *testing.T) {
	s := newSwapTree(t)
	steps := realSteps()
	steps.mkdir = func(dir string, perm fs.FileMode) error {
		if err := os.Mkdir(dir, perm); err != nil {
			return err
		}
		s.swapByRemoval(t)
		return nil
	}
	steps.create = noCreate(t)
	if err := writeWith(t, steps, s.dir, swapKey()); !errors.Is(err, ErrKeyDirMoved) {
		t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
	}
	s.untouched(t)
}

func TestWriteKeyPairRefusesALinkSwappedInBeforeItsHandle(t *testing.T) {
	s := newSwapTree(t)
	steps := realSteps()
	steps.openRoot = func(dir string) (*os.Root, error) {
		s.swapByRename(t)
		return os.OpenRoot(dir)
	}
	steps.create = noCreate(t)
	if err := writeWith(t, steps, s.dir, swapKey()); !errors.Is(err, ErrKeyDirMoved) {
		t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
	}
	s.untouched(t)
}

// TestWriteKeyPairRefusesAHandleToAnotherDirectory: the link is in place only
// while the handle is opened, and the directory made is back at the path
// when it is checked, so only the handle itself shows the swap.
func TestWriteKeyPairRefusesAHandleToAnotherDirectory(t *testing.T) {
	s := newSwapTree(t)
	steps := realSteps()
	steps.openRoot = func(dir string) (*os.Root, error) {
		s.swapByRename(t)
		root, err := os.OpenRoot(dir)
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(s.aside, dir); err != nil {
			t.Fatal(err)
		}
		return root, err
	}
	steps.create = noCreate(t)
	if err := writeWith(t, steps, s.dir, swapKey()); !errors.Is(err, ErrKeyDirMoved) {
		t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
	}
	s.untouched(t)
}

// TestWriteKeyPairWritesNothingThroughALinkSwappedInAtTheFirstCreate: the
// directory made is removed and a link put in its place; the handle reaches
// the removed directory, which takes no file.
func TestWriteKeyPairWritesNothingThroughALinkSwappedInAtTheFirstCreate(t *testing.T) {
	s := newSwapTree(t)
	steps := realSteps()
	create := steps.create
	swapped := false
	steps.create = func(root *os.Root, name string, body []byte, perm fs.FileMode) error {
		if !swapped {
			swapped = true
			s.swapByRemoval(t)
		}
		return create(root, name, body, perm)
	}
	if err := writeWith(t, steps, s.dir, swapKey()); err == nil {
		t.Error("WriteKeyPair reported a key pair written into a directory the path no longer names")
	}
	s.untouched(t)
}

// TestWriteKeyPairRefusesADirectoryMovedWhileWriting: the directory made is
// moved aside and a link put at the path while the files are written. The
// handle keeps writing into the moved directory; the check after the writes
// refuses, and what was written is removed from it.
func TestWriteKeyPairRefusesADirectoryMovedWhileWriting(t *testing.T) {
	s := newSwapTree(t)
	steps := realSteps()
	create := steps.create
	steps.create = func(root *os.Root, name string, body []byte, perm fs.FileMode) error {
		if name == PublicFile {
			s.swapByRename(t)
		}
		return create(root, name, body, perm)
	}
	if err := writeWith(t, steps, s.dir, swapKey()); !errors.Is(err, ErrKeyDirMoved) {
		t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
	}
	s.untouched(t)
	entries, err := os.ReadDir(s.aside)
	if err != nil || len(entries) != 0 {
		t.Errorf("the moved directory holds %v (%v), want nothing", entries, err)
	}
}
