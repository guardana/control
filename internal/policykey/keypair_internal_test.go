package policykey

import (
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// realSteps are the steps WriteKeyPair takes, for a test to replace one. A
// replaced step that still has to write calls the one it replaced.
func realSteps() keyPairSteps { return osSteps }

// writeWith runs WriteKeyPair, the entry point keygen calls, with steps in
// place of the ones it takes, so a test drives the wiring keygen runs.
func writeWith(t *testing.T, steps keyPairSteps, dir string, key ed25519.PrivateKey) error {
	t.Helper()
	saved := osSteps
	osSteps = steps
	defer func() { osSteps = saved }()
	return WriteKeyPair(dir, key)
}

// TestWriteKeyPairRemovesWhatItMadeOnFailure: a failure after the private
// half is written leaves no directory, so no private key survives whose public
// half nobody printed.
func TestWriteKeyPairRemovesWhatItMadeOnFailure(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	refused := errors.New("the disk is full")
	steps := realSteps()
	create := steps.create
	steps.create = func(root *os.Root, name string, body []byte, perm fs.FileMode) error {
		if name == PublicFile {
			return refused
		}
		return create(root, name, body, perm)
	}
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	if err := writeWith(t, steps, dir, key); !errors.Is(err, refused) {
		t.Fatalf("WriteKeyPair = %v, want the create's refusal", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directory survived the failure: %v", err)
	}
}

// TestWriteKeyPairRemovesTheDirectoryWhenNoHandleOpens: the directory made is
// removed when the handle to it cannot be opened, so the next keygen does not
// meet an empty directory it would refuse.
func TestWriteKeyPairRemovesTheDirectoryWhenNoHandleOpens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	refused := errors.New("too many open files")
	steps := realSteps()
	steps.openRoot = func(string) (*os.Root, error) { return nil, refused }
	if err := writeWith(t, steps, dir, ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))); !errors.Is(err, refused) {
		t.Fatalf("WriteKeyPair = %v, want the open's refusal", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directory survived the failure: %v", err)
	}
}

// TestWriteKeyPairRemovesTheDirectoryWhenItsModeCannotBeSet: a failure after
// the directory made was opened and checked removes it too.
func TestWriteKeyPairRemovesTheDirectoryWhenItsModeCannotBeSet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	refused := errors.New("read-only file system")
	steps := realSteps()
	steps.chmod = func(*os.File, fs.FileMode) error { return refused }
	steps.create = func(*os.Root, string, []byte, fs.FileMode) error {
		t.Error("a file was created in a directory whose mode was never set")
		return errors.New("refused by the test")
	}
	if err := writeWith(t, steps, dir, ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))); !errors.Is(err, refused) {
		t.Fatalf("WriteKeyPair = %v, want the chmod's refusal", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directory survived the failure: %v", err)
	}
}
