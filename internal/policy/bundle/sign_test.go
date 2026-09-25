package bundle_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/policy/bundle"
)

// Seeds, public keys and signatures of RFC 8032, section 7.1, TEST 1 and
// TEST 2, typed with the RFC's own line breaks. rfcKey holds each seed to its
// public key through the library, so a typing error fails there and cannot
// pass for a refusal anywhere else.
const (
	rfc1Seed = "9d61b19deffd5a60ba844af492ec2cc4" +
		"4449c5697b326919703bac031cae7f60"
	rfc1Pub = "d75a980182b10ab7d54bfed3c964073a" +
		"0ee172f3daa62325af021a68f707511a"
	// Over the empty message.
	rfc1Sig = "e5564300c360ac729086e2cc806e828a" +
		"84877f1eb8e5d974d873e06522490155" +
		"5fb8821590a33bacc61e39701cf9b46b" +
		"d25bf5f0595bbe24655141438e7a100b"

	rfc2Seed = "4ccd089b28ff96da9db6c346ec114e0f" +
		"5b8a319f35aba624da8cf6ed4fb8a6fb"
	rfc2Pub = "3d4017c3e843895a92b70aa74d1b7ebc" +
		"9c982ccf2ec4968cc0cd55f12af4660c"
	// Over the one byte 0x72.
	rfc2Sig = "92a009a9f0d4cab8720e820b5f642540" +
		"a2b27b5416503f8fb3762223ebdb69da" +
		"085ac1e43e15996e458f3613d0f11d8c" +
		"387b2eaeb4302aeeb00d291612bb0c00"
)

// A body and its encoding under PayloadType, the second typed from the
// formula with the length counted by hand.
const (
	policyBody       = `{"a":1}`
	signedPolicyBody = `DSSEv1 33 application/vnd.agent-policy+json 7 {"a":1}`
)

type testKey struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func rfcKey(tb testing.TB, seedHex, pubHex string) testKey {
	tb.Helper()
	seed := unhex(tb, seedHex)
	if len(seed) != ed25519.SeedSize {
		tb.Fatalf("seed %s is %d bytes", seedHex, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := ed25519.PublicKey(unhex(tb, pubHex))
	if !bytes.Equal(priv[ed25519.SeedSize:], pub) {
		tb.Fatalf("seed %s derives public key %x; RFC 8032 prints %s", seedHex, priv[ed25519.SeedSize:], pubHex)
	}
	return testKey{priv: priv, pub: pub}
}

func unhex(tb testing.TB, s string) []byte {
	tb.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		tb.Fatalf("hex %q: %v", s, err)
	}
	return b
}

// The value ADR-0011 fixes for signature_alg, typed from the record. Nothing
// in this package reads the constant; the loader compares the wire's field
// with it, so an edit here would change what every bundle has to carry.
func TestSignatureAlgIsTheRecordsValue(t *testing.T) {
	if got := bundle.SignatureAlg; got != "ed25519-dsse" {
		t.Fatalf("SignatureAlg = %q, want %q", got, "ed25519-dsse")
	}
}

// What SignBytes signs is the encoding of the policy type and the body, as the
// formula gives it, and neither the body nor another type's encoding. Ed25519
// is deterministic, so the library's signature over the typed bytes is the
// only right answer. The check through PAE is the lane brief's own wording.
func TestSignBytesSignsTheEncodingOfThePolicyType(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	sig, err := bundle.SignBytes([]byte(policyBody), k.priv)
	if err != nil {
		t.Fatalf("SignBytes: %v", err)
	}
	if !ed25519.Verify(k.pub, bundle.PAE(bundle.PayloadType, []byte(policyBody)), sig) {
		t.Errorf("the signature does not verify over PAE(PayloadType, body)")
	}
	if want := ed25519.Sign(k.priv, []byte(signedPolicyBody)); !bytes.Equal(sig, want) {
		t.Errorf("SignBytes = %x, want the library's signature over %q, %x", sig, signedPolicyBody, want)
	}
	if ed25519.Verify(k.pub, []byte(policyBody), sig) {
		t.Errorf("the signature verifies over the bare body")
	}
}

// SignBytes over a largeBody: the library verifies the signature over the
// typed encoding of the whole body, and once the last byte changes,
// VerifyBytes refuses it. A signature over any prefix of the body up to the
// parser's bound fails both checks. It would let whoever holds one signed
// bundle rewrite everything past the prefix.
func TestSignBytesCoversTheLastByteOfALargeBody(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	keys := bundle.Keyring{"k1": k.pub}
	body := largeBody()
	sig, err := bundle.SignBytes(body, k.priv)
	if err != nil {
		t.Fatalf("SignBytes over %d bytes: %v", len(body), err)
	}
	if !ed25519.Verify(k.pub, append([]byte(largePrefix), body...), sig) {
		t.Errorf("SignBytes over %d bytes: the signature does not verify over the encoding of the whole body", len(body))
	}
	checkOnly(t, bundle.VerifyBytes(body, sig, "k1", keys), nil, "VerifyBytes over the %d bytes signed", len(body))
	changed := bytes.Clone(body)
	changed[len(changed)-1]++
	checkOnly(t, bundle.VerifyBytes(changed, sig, "k1", keys), bundle.ErrSignature,
		"the last of %d bytes changed after signing: VerifyBytes", len(changed))
}

// crypto/ed25519 panics on a private key that is not 64 bytes, so SignBytes
// refuses one before the library sees it. Without the check every row panics,
// and nothing here recovers a panic. The whole key after the rows is the
// boundary's other side.
func TestSignBytesRefusesAPrivateKeyOfTheWrongSize(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	cases := []struct {
		name string
		key  ed25519.PrivateKey
	}{
		{"nil", nil},
		{"empty", ed25519.PrivateKey{}},
		{"the seed alone", k.priv[:ed25519.SeedSize]},
		{"one byte short", k.priv[:ed25519.PrivateKeySize-1]},
		{"one byte over", append(bytes.Clone(k.priv), 0)},
		{"twice over", append(bytes.Clone(k.priv), k.priv...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig, err := bundle.SignBytes([]byte(policyBody), tc.key)
			checkOnly(t, err, bundle.ErrKeySize, "SignBytes with %d bytes of key", len(tc.key))
			if sig != nil {
				t.Errorf("SignBytes with %d bytes of key refused and still returned %x", len(tc.key), sig)
			}
		})
	}
	if _, err := bundle.SignBytes([]byte(policyBody), k.priv); err != nil {
		t.Fatalf("SignBytes with the whole %d-byte key: %v", len(k.priv), err)
	}
}

// A private key carries its public half, and the library signs with whatever
// half it is handed without checking it against the seed. Each row shows the
// library signing without complaint and neither key verifying the result, so
// the refusal is SignBytes's own. Signing one message under one seed with two
// halves would also give away the private scalar, because the nonce depends on
// the seed and the message alone.
func TestSignBytesRefusesAKeyWhosePublicHalfIsNotItsSeeds(t *testing.T) {
	k1 := rfcKey(t, rfc1Seed, rfc1Pub)
	k2 := rfcKey(t, rfc2Seed, rfc2Pub)
	flipped := bytes.Clone(k1.priv)
	flipped[ed25519.PrivateKeySize-1] ^= 0x01
	cases := []struct {
		name string
		key  ed25519.PrivateKey
	}{
		{"one bit of the public half flipped", flipped},
		{"another key's public half", append(bytes.Clone(k1.priv[:ed25519.SeedSize]), k2.pub...)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			made := ed25519.Sign(tc.key, []byte(signedPolicyBody))
			half := ed25519.PublicKey(tc.key[ed25519.SeedSize:])
			if ed25519.Verify(k1.pub, []byte(signedPolicyBody), made) || ed25519.Verify(half, []byte(signedPolicyBody), made) {
				t.Fatalf("a key verifies what the library signed with a mismatched half; the premise of this test is gone")
			}
			sig, err := bundle.SignBytes([]byte(policyBody), tc.key)
			checkOnly(t, err, bundle.ErrKeyPair, "SignBytes")
			if sig != nil {
				t.Errorf("SignBytes refused and still returned %x", sig)
			}
		})
	}
	if _, err := bundle.SignBytes([]byte(policyBody), k1.priv); err != nil {
		t.Fatalf("SignBytes with the key both halves of which agree: %v", err)
	}
}

// Whatever bytes arrive as a private key, SignBytes signs only with a whole key
// whose halves agree, and otherwise refuses with the sentinel that names what
// is wrong. Nothing here recovers a panic.
func TestSignBytesOnAnyKeyBytes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		key := drawPrivateKey(rt)
		body := rapid.SliceOfN(rapid.Byte(), 0, 64).Draw(rt, "body")
		sig, err := bundle.SignBytes(body, key)
		if !checkOnly(rt, err, expectSign(key), "SignBytes with key %x", key) {
			return
		}
		if err != nil {
			if sig != nil {
				rt.Fatalf("SignBytes refused with %v and still returned %x", err, sig)
			}
			return
		}
		pub := ed25519.PublicKey(key[ed25519.SeedSize:])
		if !ed25519.Verify(pub, specPAE(bundle.PayloadType, body), sig) {
			rt.Fatalf("the signature does not verify over the encoding of %q", body)
		}
	})
}

func drawPrivateKey(rt *rapid.T) ed25519.PrivateKey {
	switch rapid.IntRange(0, 2).Draw(rt, "shape") {
	case 0:
		return rapid.SliceOfN(rapid.Byte(), 0, 96).Draw(rt, "any length")
	case 1:
		return rapid.SliceOfN(rapid.Byte(), ed25519.PrivateKeySize, ed25519.PrivateKeySize).Draw(rt, "whole length")
	default:
		return ed25519.NewKeyFromSeed(rapid.SliceOfN(rapid.Byte(), ed25519.SeedSize, ed25519.SeedSize).Draw(rt, "seed"))
	}
}

// expectSign is the refusal SignBytes owes a key, worked out without calling
// it.
func expectSign(key ed25519.PrivateKey) error {
	if len(key) != ed25519.PrivateKeySize {
		return bundle.ErrKeySize
	}
	derived := ed25519.NewKeyFromSeed(key[:ed25519.SeedSize])
	if !bytes.Equal(derived[ed25519.SeedSize:], key[ed25519.SeedSize:]) {
		return bundle.ErrKeyPair
	}
	return nil
}
