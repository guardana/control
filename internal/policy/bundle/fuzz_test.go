package bundle_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"maps"
	"slices"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
)

// FuzzVerifyBytes hands VerifyBytes any body, signature, key id and key, the
// key pinned under "fuzzed" beside a real one under "pinned". It must not
// panic, must leave its inputs and the keyring as they were, and must return
// what expectVerify works out without calling anything in this package, and
// match no other sentinel.
func FuzzVerifyBytes(f *testing.F) {
	k := rfcKey(f, rfc1Seed, rfc1Pub)
	body := []byte(policyBody)
	good := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	forgery := append(unhex(f, identityY), make([]byte, 32)...)

	f.Add(body, good, "pinned", []byte(nil))
	f.Add(body, good, "fuzzed", []byte(k.pub))
	f.Add(body, ed25519.Sign(k.priv, body), "pinned", []byte(nil))
	f.Add(body, ed25519.Sign(k.priv, []byte(`DSSEv1 16 application/json 7 {"a":1}`)), "pinned", []byte(nil))
	f.Add(body, good[:ed25519.SignatureSize-1], "pinned", []byte(nil))
	f.Add(body, good, "", []byte(k.pub))
	f.Add(body, good, "Pinned", []byte(nil))
	f.Add(body, good, "fuzzed", []byte(k.priv))
	f.Add([]byte("0"), forgery, "fuzzed", unhex(f, identityY))
	// A weak key with a signature the library refuses under it: the key is
	// refused first, so the error names the key.
	f.Add([]byte("0"), make([]byte, ed25519.SignatureSize), "fuzzed", unhex(f, identityY))
	f.Add([]byte{}, []byte{}, "", []byte{})

	f.Fuzz(func(t *testing.T, canonical, signature []byte, keyID string, key []byte) {
		keys := bundle.Keyring{"pinned": k.pub, "fuzzed": key}
		pinned := cloneKeyring(keys)
		held := [][]byte{bytes.Clone(canonical), bytes.Clone(signature), bytes.Clone(key)}
		want := expectVerify(canonical, signature, keyID, keys)

		err := bundle.VerifyBytes(canonical, signature, keyID, keys)
		checkOnly(t, err, want, "VerifyBytes(%q, %x, %q, key %x)", canonical, signature, keyID, key)
		for i, in := range [][]byte{canonical, signature, key} {
			if !bytes.Equal(in, held[i]) {
				t.Fatalf("VerifyBytes changed argument %d", i)
			}
		}
		if !sameKeyring(keys, pinned) {
			t.Fatalf("VerifyBytes changed the keyring")
		}
	})
}

// expectVerify is what VerifyBytes owes, worked out without it: an id that
// selects a key, a key of 32 bytes and not of small order, and a signature the
// library verifies over specPAE's encoding.
func expectVerify(canonical, signature []byte, keyID string, keys bundle.Keyring) error {
	key, ok := keys[keyID]
	switch {
	case !ok || keyID == "":
		return bundle.ErrUnknownKey
	case len(key) != ed25519.PublicKeySize:
		return bundle.ErrKeySize
	case isSmallOrder(key):
		return bundle.ErrWeakKey
	case !ed25519.Verify(key, specPAE(bundle.PayloadType, canonical), signature):
		return bundle.ErrSignature
	}
	return nil
}

func isSmallOrder(key ed25519.PublicKey) bool {
	y := bytes.Clone(key)
	y[len(y)-1] &= 0x7f
	return slices.Contains(smallOrderY(), hex.EncodeToString(y))
}

// cloneKeyring copies a keyring and every key in it.
func cloneKeyring(keys bundle.Keyring) bundle.Keyring {
	out := make(bundle.Keyring, len(keys))
	for id, key := range keys {
		out[id] = bytes.Clone(key)
	}
	return out
}

// sameKeyring reports whether two keyrings pin the same bytes under the same
// ids.
func sameKeyring(a, b bundle.Keyring) bool {
	return maps.EqualFunc(a, b, func(x, y ed25519.PublicKey) bool { return bytes.Equal(x, y) })
}
