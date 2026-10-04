//go:build unix

package approvals_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/approvals"
)

// TestADirectoryAnotherAccountOwnsIsRefused: write access to the directory is
// the approval authority, so one another account owns is that account's
// approvals. Both handles refuse it, and a plane refused over an empty one
// leaves no marker behind.
func TestADirectoryAnotherAccountOwnsIsRefused(t *testing.T) {
	store := filepath.Join(t.TempDir(), "approvals")
	empty := filepath.Join(t.TempDir(), "empty")
	for _, d := range []string{store, empty} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	p, err := approvals.OpenPlane(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []int{os.Geteuid() + 1, -1} {
		refusedAs(t, uid, store, empty)
	}
	if _, err := os.Lstat(filepath.Join(empty, "store.meta")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused plane wrote the marker: %v", err)
	}
	a, err := approvals.OpenApprover(store)
	if err != nil {
		t.Fatalf("OpenApprover as the owner = %v", err)
	}
	if err := a.Close(); err != nil {
		t.Error(err)
	}
}

// refusedAs opens store with both handles, and empty with a plane, as if
// this process ran as uid, and expects each refused with ErrOwner.
func refusedAs(t *testing.T, uid int, store, empty string) {
	t.Helper()
	defer approvals.SetEffectiveUID(func() int { return uid })()
	if p, err := approvals.OpenPlane(store); !errors.Is(err, approvals.ErrOwner) {
		t.Errorf("OpenPlane as uid %d = %v, want ErrOwner", uid, err)
		closeIfOpen(t, p)
	}
	if a, err := approvals.OpenApprover(store); !errors.Is(err, approvals.ErrOwner) {
		t.Errorf("OpenApprover as uid %d = %v, want ErrOwner", uid, err)
		if a != nil {
			_ = a.Close()
		}
	}
	if p, err := approvals.OpenPlane(empty); !errors.Is(err, approvals.ErrOwner) {
		t.Errorf("OpenPlane over an empty directory as uid %d = %v, want ErrOwner", uid, err)
		closeIfOpen(t, p)
	}
}

func closeIfOpen(t *testing.T, p *approvals.Plane) {
	t.Helper()
	if p != nil {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	}
}
