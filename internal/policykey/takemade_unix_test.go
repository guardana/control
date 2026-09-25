//go:build unix

package policykey

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

// What takeMade finds at the path right after Mkdir, when something else was
// put there: each is refused, and each is left as it was.

// replaceAfterMkdir is a mkdir step that makes the directory and then lets put
// replace it.
func replaceAfterMkdir(put func(dir string) error) func(string, fs.FileMode) error {
	return func(dir string, perm fs.FileMode) error {
		if err := os.Mkdir(dir, perm); err != nil {
			return err
		}
		return put(dir)
	}
}

// TestWriteKeyPairRefusesADirectorySwappedInAfterMkdir: a directory of the same
// account, holding a file and open to others, is moved to the path. It is not
// the directory made: nothing is written into it and its mode stays.
func TestWriteKeyPairRefusesADirectorySwappedInAfterMkdir(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "keys")
	shared := filepath.Join(parent, "shared")
	if err := os.Mkdir(shared, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o755); err != nil { //nolint:gosec // G302: a directory left open to others, as the swap needs
		t.Fatal(err)
	}
	steps := realSteps()
	steps.mkdir = replaceAfterMkdir(func(d string) error {
		if err := os.Rename(d, filepath.Join(parent, "aside")); err != nil {
			return err
		}
		return os.Rename(shared, d)
	})
	steps.create = noCreate(t)
	if err := writeWith(t, steps, dir, swapKey()); !errors.Is(err, ErrKeyDirMoved) {
		t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "README" {
		t.Errorf("the directory swapped in holds %v (%v), want its README alone", entries, err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("the directory swapped in: mode %v (%v), want 0755", info.Mode().Perm(), err)
	}
}

// TestWriteKeyPairRefusesADirectoryOfAnotherAccount names another account as
// the one running keygen, so the directory it made reads as someone else's:
// it is neither written into, nor changed, nor removed.
func TestWriteKeyPairRefusesADirectoryOfAnotherAccount(t *testing.T) {
	t.Cleanup(func() { effectiveUID = os.Geteuid })
	for name, c := range map[string]struct {
		uid  int
		want error
	}{
		"another account":       {os.Geteuid() + 1, files.ErrOwner},
		"no account to compare": {-1, files.ErrOwnerUnknown},
	} {
		effectiveUID = func() int { return c.uid }
		steps := realSteps()
		steps.create = noCreate(t)
		steps.chmod = func(*os.File, fs.FileMode) error {
			t.Errorf("%s: the mode of a directory keygen does not own was changed", name)
			return errors.New("refused by the test")
		}
		dir := filepath.Join(t.TempDir(), "keys")
		err := writeWith(t, steps, dir, swapKey())
		if !errors.Is(err, ErrKeyDirMoved) || !errors.Is(err, c.want) {
			t.Errorf("%s: WriteKeyPair = %v, want ErrKeyDirMoved and the owner's refusal", name, err)
		}
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			t.Errorf("%s: the directory was not left there: %v", name, err)
		}
	}
}

// TestWriteKeyPairRefusesAPipeSwappedInAfterMkdir: a named pipe at the path
// is refused at once, never waited on for a writer, and left there.
func TestWriteKeyPairRefusesAPipeSwappedInAfterMkdir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	steps := realSteps()
	steps.mkdir = replaceAfterMkdir(func(d string) error {
		if err := os.Remove(d); err != nil {
			return err
		}
		return syscall.Mkfifo(d, 0o600)
	})
	steps.create = noCreate(t)
	done := make(chan error, 1)
	go func() { done <- writeWith(t, steps, dir, swapKey()) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrKeyDirMoved) {
			t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
		}
	case <-time.After(5 * time.Second):
		w, err := os.OpenFile(dir, os.O_WRONLY, 0) //nolint:gosec // G304: the test's own pipe, opened to release the reader
		if err == nil {
			_ = w.Close()
		}
		<-done
		t.Fatal("WriteKeyPair waited on a named pipe for a writer")
	}
	if info, err := os.Lstat(dir); err != nil || info.Mode()&fs.ModeNamedPipe == 0 {
		t.Errorf("the pipe was not left there: %v", err)
	}
}

// TestWriteKeyPairLeavesAFileSwappedInAfterMkdir: a file no one can open is
// put at the path. The cleanup of a directory that could not be opened
// removes only a directory, so the file stays.
func TestWriteKeyPairLeavesAFileSwappedInAfterMkdir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	steps := realSteps()
	steps.mkdir = replaceAfterMkdir(func(d string) error {
		if err := os.Remove(d); err != nil {
			return err
		}
		return os.WriteFile(d, []byte("theirs"), 0)
	})
	steps.create = noCreate(t)
	if err := writeWith(t, steps, dir, swapKey()); !errors.Is(err, ErrKeyDirMoved) {
		t.Errorf("WriteKeyPair = %v, want ErrKeyDirMoved", err)
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len("theirs")) {
		t.Errorf("the file put at the path did not survive: %v, %v", info, err)
	}
}
