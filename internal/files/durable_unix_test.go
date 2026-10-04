//go:build unix

package files_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/files"
)

// TestACreateThatCannotBeMadeDurableLeavesNoName: the name is linked, and then
// the directory cannot be opened to force it to disk. The create reports the
// failure and the name is gone with it, so the next create of the name is not
// refused as taken.
func TestACreateThatCannotBeMadeDurableLeavesNoName(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the superuser opens a directory whatever its mode")
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := os.Chmod(dir, 0o300); err != nil { //nolint:gosec // G302: write and search without read, so only the directory's own open fails
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) }) //nolint:gosec // G302: as t.TempDir leaves it
	err = files.CreateNoReplaceIn(root, "name", []byte("body"), 0o600)
	if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil { //nolint:gosec // G302: as t.TempDir leaves it
		t.Fatal(chmodErr)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("CreateNoReplaceIn with a directory it cannot force to disk = %v, want fs.ErrPermission", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("a failed create left %v, %v", entries, err)
	}
	if err := files.CreateNoReplaceIn(root, "name", []byte("body"), 0o600); err != nil {
		t.Fatalf("the create after the failed one: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "name")); err != nil || string(got) != "body" { //nolint:gosec // G304: the test's own temporary directory
		t.Fatalf("the file written: %q, %v", got, err)
	}
}
