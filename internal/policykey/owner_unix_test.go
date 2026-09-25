//go:build unix

package policykey_test

import (
	"errors"
	"os"
	"testing"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policykey"
)

// TestReadPrivateRefusesAKeyOfAnotherAccount: a 0600 key file another account
// owns is that account's key. Giving a file away needs root, so this runs only
// as root; internal/files tests the owner check without it.
func TestReadPrivateRefusesAKeyOfAnotherAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving the key file to another account needs root")
	}
	good, err := policykey.MarshalPrivate(rfcKey(t))
	if err != nil {
		t.Fatal(err)
	}
	path := writeWithMode(t, good, 0o600)
	if err := os.Chown(path, 1, -1); err != nil {
		t.Fatalf("chown: %v", err)
	}
	key, err := policykey.ReadPrivate(path)
	if key != nil || !errors.Is(err, policykey.ErrKeyFileOwner) || !errors.Is(err, files.ErrOwner) {
		t.Errorf("ReadPrivate(a key uid 1 owns) = %v, want ErrKeyFileOwner", err)
	}
}
