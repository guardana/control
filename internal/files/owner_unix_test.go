//go:build unix

package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// statInfo is a FileInfo whose Sys is whatever the test hands it.
type statInfo struct{ sys any }

func (statInfo) Name() string       { return "k" }
func (statInfo) Size() int64        { return 0 }
func (statInfo) Mode() fs.FileMode  { return 0o600 }
func (statInfo) ModTime() time.Time { return time.Time{} }
func (statInfo) IsDir() bool        { return false }
func (i statInfo) Sys() any         { return i.sys }

func TestCheckOwner(t *testing.T) {
	for name, c := range map[string]struct {
		info fs.FileInfo
		uid  int
		want error
	}{
		"the owner":            {statInfo{&syscall.Stat_t{Uid: 501}}, 501, nil},
		"another account":      {statInfo{&syscall.Stat_t{Uid: 502}}, 501, ErrOwner},
		"root's file, not me":  {statInfo{&syscall.Stat_t{Uid: 0}}, 501, ErrOwner},
		"root's file, as root": {statInfo{&syscall.Stat_t{Uid: 0}}, 0, nil},
		"no stat at all":       {statInfo{nil}, 501, ErrOwnerUnknown},
		"a nil stat":           {statInfo{(*syscall.Stat_t)(nil)}, 501, ErrOwnerUnknown},
	} {
		err := checkOwner(c.info, c.uid)
		if (c.want == nil) != (err == nil) || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: checkOwner = %v, want %v", name, err, c.want)
		}
	}
}

// TestReadOwnedRefusesAnotherOwner names a uid that is not the file's owner,
// which needs no second account: a file this user owns reads as another's.
func TestReadOwnedRefusesAnotherOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadOwned(path, 16, 0o077, os.Geteuid()+1); !errors.Is(err, ErrOwner) {
		t.Errorf("ReadOwned for another uid = %v, want ErrOwner", err)
	}
	if _, err := ReadOwned(path, 16, 0o077, -1); !errors.Is(err, ErrOwnerUnknown) {
		t.Errorf("ReadOwned for no uid = %v, want ErrOwnerUnknown", err)
	}
	if raw, err := ReadOwned(path, 16, 0o077, os.Geteuid()); err != nil || string(raw) != "body" {
		t.Errorf("ReadOwned of the user's own file = %q, %v", raw, err)
	}
	if raw, err := ReadRegular(path, 16, 0o077); err != nil || string(raw) != "body" {
		t.Errorf("ReadRegular, which checks no owner = %q, %v", raw, err)
	}
}

func TestCheckOwnedBy(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckOwnedBy(info, os.Geteuid()); err != nil {
		t.Errorf("CheckOwnedBy(the user's own directory) = %v", err)
	}
	if err := CheckOwnedBy(info, os.Geteuid()+1); !errors.Is(err, ErrOwner) {
		t.Errorf("CheckOwnedBy for another uid = %v, want ErrOwner", err)
	}
	if err := CheckOwnedBy(info, -1); !errors.Is(err, ErrOwnerUnknown) {
		t.Errorf("CheckOwnedBy for no uid = %v, want ErrOwnerUnknown", err)
	}
}

// TestReadOwnedRefusesAFileOfAnotherAccount needs an account that can give a
// file away, which only root is.
func TestReadOwnedRefusesAFileOfAnotherAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving a file to another account needs root; TestReadOwnedRefusesAnotherOwner covers the check without it")
	}
	path := filepath.Join(t.TempDir(), "k")
	if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 1, -1); err != nil {
		t.Fatalf("chown: %v", err)
	}
	if _, err := ReadOwned(path, 16, 0o077, os.Geteuid()); !errors.Is(err, ErrOwner) {
		t.Errorf("ReadOwned of a file uid 1 owns = %v, want ErrOwner", err)
	}
}
