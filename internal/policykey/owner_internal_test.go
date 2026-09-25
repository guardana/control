//go:build unix

package policykey

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/files"
)

// TestReadPrivateRefusesAKeyAnotherAccountOwns names another account as the
// one running the command, so the key file this user wrote reads as someone
// else's: the check is exercised without root.
func TestReadPrivateRefusesAKeyAnotherAccountOwns(t *testing.T) {
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	raw, err := MarshalPrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), PrivateFile)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadPrivate(path); err != nil || !got.Equal(key) {
		t.Fatalf("ReadPrivate as the file's owner = %v, want the key", err)
	}
	t.Cleanup(func() { effectiveUID = os.Geteuid })
	effectiveUID = func() int { return os.Geteuid() + 1 }
	got, err := ReadPrivate(path)
	if got != nil || !errors.Is(err, ErrKeyFileOwner) || !errors.Is(err, files.ErrOwner) {
		t.Errorf("ReadPrivate as another account = %v, want ErrKeyFileOwner", err)
	}
	effectiveUID = func() int { return -1 }
	if _, err := ReadPrivate(path); !errors.Is(err, files.ErrOwnerUnknown) {
		t.Errorf("ReadPrivate with no account to compare = %v, want ErrOwnerUnknown", err)
	}
}
