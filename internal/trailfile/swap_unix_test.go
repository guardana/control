//go:build unix

package trailfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestAFileSwappedInAsItIsOpenedIsRefused: the file judged by its directory
// entry is replaced, between that look and the open, by another file of this
// account. The open is refused, and the file that came in keeps its torn last
// line, since nothing the writer judged leads to it.
func TestAFileSwappedInAsItIsOpenedIsRefused(t *testing.T) {
	path := trailPath(t)
	writeFile(t, path, firstLine+tornTail)
	other := filepath.Join(filepath.Dir(path), "other.jsonl")
	writeFile(t, other, firstLine+tornTail)
	ops := osOps
	ops.open = func(root *os.Root, name string, flag int, perm fs.FileMode) (*os.File, error) {
		if err := os.Rename(other, path); err != nil {
			return nil, err
		}
		return root.OpenFile(name, flag, perm)
	}
	w, err := open(path, ops)
	if !errors.Is(err, ErrChanged) || w != nil {
		t.Errorf("Open of a file swapped as it was opened = %v, %v; want ErrChanged", w, err)
	}
	if got := contents(t, path); got != firstLine+tornTail {
		t.Errorf("the file swapped in holds %q after the refused open", got)
	}
}
