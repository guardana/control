//go:build unix

package policykey_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/guardana/control/internal/policykey"
)

// TestWriteKeyPairUnderANarrowUmask: a umask that takes the owner's write or
// search bit would leave Mkdir's directory unwritable; the directory is set to
// 0700 on purpose, and the files get their own modes whatever the umask.
func TestWriteKeyPairUnderANarrowUmask(t *testing.T) {
	for _, mask := range []int{0o277, 0o377} {
		dir := filepath.Join(t.TempDir(), "keys")
		old := syscall.Umask(mask)
		err := policykey.WriteKeyPair(dir, rfcKey(t))
		syscall.Umask(old)
		if err != nil {
			t.Fatalf("WriteKeyPair under umask %04o: %v", mask, err)
		}
		for name, want := range map[string]os.FileMode{"": 0o700, "signing.key": 0o600, "signing.pub": 0o644} {
			if got := modeOf(t, filepath.Join(dir, name)); got != want {
				t.Errorf("umask %04o, %q: mode %04o, want %04o", mask, name, got, want)
			}
		}
	}
}

// TestWriteKeyPairUnderAUmaskOfEverything: Mkdir leaves a directory with no
// permission bits at all, which the owner cannot open to take hold of. The
// refusal leaves nothing at the path, so the next run is not refused for a
// directory this one made.
func TestWriteKeyPairUnderAUmaskOfEverything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keys")
	old := syscall.Umask(0o777)
	err := policykey.WriteKeyPair(dir, rfcKey(t))
	syscall.Umask(old)
	if err == nil {
		t.Fatal("WriteKeyPair under umask 0777 reported a key pair written")
	}
	if _, statErr := os.Lstat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("the refused run left the path behind: %v (the refusal was %v)", statErr, err)
	}
	if err := policykey.WriteKeyPair(dir, rfcKey(t)); err != nil {
		t.Errorf("the next run, under the usual umask: %v", err)
	}
}
