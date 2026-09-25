package policykey_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// The key of RFC 8032, section 7.1, TEST 1: a published test vector, so the
// public half below is the RFC's and not this package's computation.
const (
	rfcSeedHex   = "9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
	rfcPublicHex = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"
	// rfcPublicLine is standard base64 of rfcPublicHex.
	rfcPublicLine = "11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo="
	// rfcKeyID is "ed25519-" and the first 16 hex digits of SHA-256 over the
	// 32 bytes of rfcPublicHex, computed outside this code.
	rfcKeyID = "ed25519-21fe31dfa154a261"
	// rfc8410Prefix is the DER that RFC 8410 puts before the 32-byte seed of
	// an Ed25519 key in PKCS#8: a SEQUENCE of version 0, the id-Ed25519
	// algorithm identifier and an OCTET STRING wrapping an OCTET STRING.
	rfc8410Prefix = "302e020100300506032b657004220420"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return b
}

// rfcKey is built at run time from the RFC's seed; no key file is in the tree.
func rfcKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	return ed25519.NewKeyFromSeed(mustHex(t, rfcSeedHex))
}

// otherKey is a second key, for a signature under the wrong one.
func otherKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	return ed25519.NewKeyFromSeed(seed)
}

// writeWithMode writes body to a new file under a fresh directory and gives
// it perm exactly, whatever the umask.
func writeWithMode(t *testing.T, body []byte, perm os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("writing the key file: %v", err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	return path
}
