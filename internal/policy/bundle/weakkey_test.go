package bundle_test

import (
	"crypto/ed25519"
	"errors"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
)

// WeakKey names exactly the keys VerifyBytes refuses as ErrWeakKey: every
// encoding of a point of small order, with the sign bit clear and set, and
// no key of another size or of a usable point.
func TestWeakKeyIsTheVerifiersRule(t *testing.T) {
	for _, y := range smallOrderY() {
		for _, signBit := range []byte{0, 0x80} {
			key := ed25519.PublicKey(unhex(t, y))
			key[ed25519.PublicKeySize-1] |= signBit
			if !bundle.WeakKey(key) {
				t.Errorf("WeakKey(%x) = false", key)
			}
			if err := bundle.VerifyBytes([]byte("{}"), make([]byte, ed25519.SignatureSize), "k", bundle.Keyring{"k": key}); !errors.Is(err, bundle.ErrWeakKey) {
				t.Errorf("VerifyBytes under %x: %v, want ErrWeakKey", key, err)
			}
		}
	}
	usable := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	for _, key := range []ed25519.PublicKey{usable, unhex(t, identityY)[:31], nil} {
		if bundle.WeakKey(key) {
			t.Errorf("WeakKey(%x) = true", key)
		}
	}
}
