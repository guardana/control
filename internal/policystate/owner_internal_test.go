//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package policystate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestTheFloorFilesOwnOwnerIsJudged reads the floor file alone, past the
// marker, which would refuse first: no test without root can give one file
// of a directory another owner.
func TestTheFloorFilesOwnOwnerIsJudged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floors")
	if err := Init(t.Context(), dir, KindPlane, "bundle-a"); err != nil {
		t.Fatal(err)
	}
	d, err := openDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.close() }()
	if _, err := d.readRecord("bundle-a"); err != nil {
		t.Fatalf("readRecord as the owner: %v", err)
	}
	restore := SetEffectiveUID(func() int { return os.Geteuid() + 1 })
	defer restore()
	if _, err := d.readRecord("bundle-a"); !errors.Is(err, ErrOwner) {
		t.Errorf("readRecord as another user: %v, want ErrOwner", err)
	}
}
