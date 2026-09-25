package bundle_test

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/policy/bundle"
)

// The accepting twin of every refusal in this file: a signature the library
// made over the typed encoding verifies. Neither SignBytes nor PAE made
// anything here.
func TestVerifyBytesAcceptsASignatureOverTheTypedEncoding(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	sig := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	if err := bundle.VerifyBytes([]byte(policyBody), sig, "k1", bundle.Keyring{"k1": k.pub}); err != nil {
		t.Fatalf("VerifyBytes: %v", err)
	}
}

// A signature over the bare body verifies under the library and is refused
// here. The first two are RFC 8032's own, typed; the library is asked about
// each first, so a typing error fails as one instead of passing as a refusal.
// The last two bodies are encodings themselves, the DSSE example's and a
// policy body's. A verifier that took such a body as already encoded would
// accept them, and a signature made for another payload type would then pass
// as a policy signature.
func TestVerifyBytesRefusesASignatureOverTheBareBody(t *testing.T) {
	k1 := rfcKey(t, rfc1Seed, rfc1Pub)
	k2 := rfcKey(t, rfc2Seed, rfc2Pub)
	dsseVector := []byte("DSSEv1 29 http://example.com/HelloWorld 11 hello world")
	cases := []struct {
		name string
		key  testKey
		body []byte
		sig  []byte
	}{
		{"RFC 8032 TEST 1, the empty message", k1, []byte{}, unhex(t, rfc1Sig)},
		{"RFC 8032 TEST 2, the byte 0x72", k2, []byte{0x72}, unhex(t, rfc2Sig)},
		{"a policy body", k1, []byte(policyBody), ed25519.Sign(k1.priv, []byte(policyBody))},
		{"the DSSE vector's encoding", k1, dsseVector, ed25519.Sign(k1.priv, dsseVector)},
		{"a policy body's encoding", k1, []byte(signedPolicyBody), ed25519.Sign(k1.priv, []byte(signedPolicyBody))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !ed25519.Verify(tc.key.pub, tc.body, tc.sig) {
				t.Fatalf("the library refuses the signature over the bare body; the vector is mistyped")
			}
			err := bundle.VerifyBytes(tc.body, tc.sig, "k", bundle.Keyring{"k": tc.key.pub})
			checkOnly(t, err, bundle.ErrSignature, "VerifyBytes")
		})
	}
}

// A signature over the same body under another payload type is refused: the
// DSSE example's type, a plain JSON type, and the policy type one byte short.
// Each encoding is typed.
func TestVerifyBytesRefusesAnotherPayloadType(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	keys := bundle.Keyring{"k1": k.pub}
	for _, tc := range []struct{ body, signed string }{
		{"hello world", "DSSEv1 29 http://example.com/HelloWorld 11 hello world"},
		{policyBody, `DSSEv1 16 application/json 7 {"a":1}`},
		{policyBody, `DSSEv1 32 application/vnd.agent-policy+jso 7 {"a":1}`},
	} {
		sig := ed25519.Sign(k.priv, []byte(tc.signed))
		err := bundle.VerifyBytes([]byte(tc.body), sig, "k1", keys)
		checkOnly(t, err, bundle.ErrSignature, "a signature over %q: VerifyBytes", tc.signed)
	}
}

// The id only selects. A signature that verifies under a pinned key is refused
// when the id does not name that key exactly, and an empty id selects nothing
// even from a keyring holding a key under it, because proto3 cannot tell an
// empty key_id from an absent one. The last check is the accepting twin.
func TestVerifyBytesRefusesAnIDThatSelectsNoPinnedKey(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	sig := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	pinned := bundle.Keyring{"k1": k.pub}
	cases := []struct {
		name string
		id   string
		keys bundle.Keyring
	}{
		{"an id nobody pinned", "k2", pinned},
		{"another case", "K1", pinned},
		{"a trailing space", "k1 ", pinned},
		{"no id", "", pinned},
		{"no id, a key pinned under none", "", bundle.Keyring{"": k.pub}},
		{"an empty keyring", "k1", bundle.Keyring{}},
		{"no keyring", "k1", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := bundle.VerifyBytes([]byte(policyBody), sig, tc.id, tc.keys)
			checkOnly(t, err, bundle.ErrUnknownKey, "VerifyBytes(id %q)", tc.id)
		})
	}
	if err := bundle.VerifyBytes([]byte(policyBody), sig, "k1", pinned); err != nil {
		t.Fatalf("VerifyBytes(id %q) = %v, want nil", "k1", err)
	}
}

// crypto/ed25519 panics on a public key that is not 32 bytes, so each row is
// refused before the library sees it; without the check each panics, and
// nothing here recovers a panic. The last row is the likeliest mistake: a
// private key pinned where its public half belongs.
func TestVerifyBytesRefusesAPinnedKeyOfTheWrongSize(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	sig := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	cases := []struct {
		name string
		key  ed25519.PublicKey
	}{
		{"nil", nil},
		{"one byte short", k.pub[:ed25519.PublicKeySize-1]},
		{"one byte over", append(bytes.Clone(k.pub), 0)},
		{"the private key", ed25519.PublicKey(k.priv)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := bundle.VerifyBytes([]byte(policyBody), sig, "k1", bundle.Keyring{"k1": tc.key})
			checkOnly(t, err, bundle.ErrKeySize, "VerifyBytes under a %d-byte key", len(tc.key))
		})
	}
	if err := bundle.VerifyBytes([]byte(policyBody), sig, "k1", bundle.Keyring{"k1": k.pub}); err != nil {
		t.Fatalf("VerifyBytes under the %d-byte key = %v, want nil", len(k.pub), err)
	}
}

// A signature by one pinned key, presented under the id of another, is refused:
// the id picks the key, and the signature has to verify under that key alone.
func TestVerifyBytesRefusesASignatureByAnotherPinnedKey(t *testing.T) {
	k1 := rfcKey(t, rfc1Seed, rfc1Pub)
	k2 := rfcKey(t, rfc2Seed, rfc2Pub)
	keys := bundle.Keyring{"k1": k1.pub, "k2": k2.pub}
	sig := ed25519.Sign(k1.priv, []byte(signedPolicyBody))
	err := bundle.VerifyBytes([]byte(policyBody), sig, "k2", keys)
	checkOnly(t, err, bundle.ErrSignature, "k1's signature under id k2: VerifyBytes")
	if err := bundle.VerifyBytes([]byte(policyBody), sig, "k1", keys); err != nil {
		t.Fatalf("k1's signature under id k1: VerifyBytes = %v, want nil", err)
	}
}

// A signature that is not 64 bytes is refused, never a panic: none, one byte
// short, one byte over, and two signatures end to end.
func TestVerifyBytesRefusesASignatureOfTheWrongLength(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	keys := bundle.Keyring{"k1": k.pub}
	sig := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	for _, s := range [][]byte{
		nil,
		sig[:ed25519.SignatureSize-1],
		append(bytes.Clone(sig), 0),
		append(bytes.Clone(sig), sig...),
	} {
		err := bundle.VerifyBytes([]byte(policyBody), s, "k1", keys)
		checkOnly(t, err, bundle.ErrSignature, "a %d-byte signature: VerifyBytes", len(s))
	}
}

// orderL is L, the order of the base point (RFC 8032, section 5.1: 2^252 +
// 27742317777372353535851937790883648493), little-endian.
const orderL = "edd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010"

// A library signature (R, S) changed in S, in two ways a lax verifier reads as
// the same signature: S + L, which the curve's math cannot tell from S since L
// is the order of the group B generates, and S with its top three bits set,
// which a verifier that masks those bits reads as S. RFC 8032 refuses both.
// Accepting either would give one bundle a second valid signature.
func TestVerifyBytesRefusesAMalleatedSignature(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	keys := bundle.Keyring{"k1": k.pub}
	sig := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	checkOnly(t, bundle.VerifyBytes([]byte(policyBody), sig, "k1", keys), nil, "the signature as the library made it: VerifyBytes")

	s := scalarLimbs(sig[32:])
	plusL, wrapped := addLimbs(s, scalarLimbs(unhex(t, orderL)))
	if wrapped || plusL[3]>>61 != 0 {
		t.Fatalf("S + L has a top bit set, so the library would refuse it before its range check")
	}
	d := curveD()
	b := basePoint(t, d)
	if sb := scalarMul(s, b, d); sb == identity() || scalarMul(plusL, b, d) != sb {
		t.Fatalf("[S + L]B is not [S]B: L as typed is not the order of B")
	}
	topBits := bytes.Clone(sig)
	topBits[ed25519.SignatureSize-1] |= 0xe0
	for _, tc := range []struct {
		name string
		sig  []byte
	}{
		{"S + L", append(bytes.Clone(sig[:32]), leBytes(plusL)...)},
		{"S with its top three bits set", topBits},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := bundle.VerifyBytes([]byte(policyBody), tc.sig, "k1", keys)
			checkOnly(t, err, bundle.ErrSignature, "%s: VerifyBytes", tc.name)
		})
	}
}

// identityY encodes the identity point, y = 1.
const identityY = "0100000000000000000000000000000000000000000000000000000000000000"

// smallOrderY lists, little-endian with the sign bit clear, the y coordinate
// of each of Ed25519's eight points of small order, and the two values at and
// above p that the library reads as 0 and 1. They were typed from memory, and
// are checked against the library, which accepts a forgery under each (the
// test below), and against the curve equation, from which
// TestSmallOrderListEqualsTheDerivedSet derives the whole set.
func smallOrderY() []string {
	return []string{
		"0000000000000000000000000000000000000000000000000000000000000000", // y = 0, order 4
		identityY, // order 1
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // y = p-1, order 2
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05", // order 8
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a", // order 8
		"edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // y = p, read as 0
		"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // y = p+1, read as 1
	}
}

// Under a key of small order the signature R = identity, S = 0 verifies for a
// share of all messages, and under the identity for every one, so a zeroed or
// otherwise degenerate key pinned by mistake would let anyone sign a bundle.
// For each encoding, with the sign bit clear and set, the library first
// accepts that forgery over some body; VerifyBytes has to refuse that body.
// The key is refused before any signature is looked at, so R = identity,
// S = 1, which the library refuses under every such key, is refused as a weak
// key too, and the error names the key.
func TestVerifyBytesRefusesAKeyOfSmallOrder(t *testing.T) {
	forgery := append(unhex(t, identityY), make([]byte, 32)...)
	refused := bytes.Clone(forgery)
	refused[32] = 1
	for _, y := range smallOrderY() {
		for _, signBit := range []byte{0, 0x80} {
			key := ed25519.PublicKey(unhex(t, y))
			key[ed25519.PublicKeySize-1] |= signBit
			t.Run(fmt.Sprintf("%x", key), func(t *testing.T) {
				body, ok := forgeableBody(key, forgery)
				if !ok {
					t.Fatalf("the library accepts the forgery over none of the bodies tried: not a key of small order")
				}
				keys := bundle.Keyring{"weak": key}
				err := bundle.VerifyBytes(body, forgery, "weak", keys)
				checkOnly(t, err, bundle.ErrWeakKey, "VerifyBytes(%q) with a forgery the library accepts", body)
				if ed25519.Verify(key, specPAE(bundle.PayloadType, body), refused) {
					t.Fatalf("the library accepts R = identity, S = 1 over %q; the premise of this row is gone", body)
				}
				err = bundle.VerifyBytes(body, refused, "weak", keys)
				checkOnly(t, err, bundle.ErrWeakKey, "VerifyBytes(%q) with a signature the library refuses", body)
			})
		}
	}
}

// forgeableBody returns the first body, counting up from "0", over whose
// encoding the library verifies forgery under key.
func forgeableBody(key ed25519.PublicKey, forgery []byte) ([]byte, bool) {
	for i := range 1000 {
		body := []byte(strconv.Itoa(i))
		if ed25519.Verify(key, specPAE(bundle.PayloadType, body), forgery) {
			return body, true
		}
	}
	return nil, false
}

// VerifyBytes writes nothing it is handed. The key has its sign bit set, which
// a check that cleared the bit in place would clear in the operator's keyring.
func TestVerifyBytesLeavesItsInputsAlone(t *testing.T) {
	key := unhex(t, identityY)
	key[ed25519.PublicKeySize-1] |= 0x80
	keys := bundle.Keyring{"weak": bytes.Clone(key)}
	body := []byte(policyBody)
	sig := append(unhex(t, identityY), make([]byte, 32)...)
	before := append(bytes.Clone(body), sig...)
	checkOnly(t, bundle.VerifyBytes(body, sig, "weak", keys), bundle.ErrWeakKey, "VerifyBytes")
	if !bytes.Equal(keys["weak"], key) {
		t.Errorf("the pinned key is now %x, was %x", keys["weak"], key)
	}
	if after := append(bytes.Clone(body), sig...); !bytes.Equal(after, before) {
		t.Errorf("the body or the signature changed")
	}
}

// Under a key from any seed, a signature SignBytes made over any body
// verifies, and a change of any one bit of the body or the signature is
// refused.
func TestVerifyBytesRefusesAnyOneBitChange(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		seed := rapid.SliceOfN(rapid.Byte(), ed25519.SeedSize, ed25519.SeedSize).Draw(rt, "seed")
		priv := ed25519.NewKeyFromSeed(seed)
		keys := bundle.Keyring{"k": ed25519.PublicKey(priv[ed25519.SeedSize:])}
		body := rapid.SliceOfN(rapid.Byte(), 0, 128).Draw(rt, "body")
		sig, err := bundle.SignBytes(body, priv)
		if err != nil {
			rt.Fatalf("SignBytes: %v", err)
		}
		if err := bundle.VerifyBytes(body, sig, "k", keys); err != nil {
			rt.Fatalf("VerifyBytes of SignBytes's own signature: %v", err)
		}
		both := append(bytes.Clone(body), sig...)
		bit := rapid.IntRange(0, 8*len(both)-1).Draw(rt, "bit")
		both[bit/8] ^= 1 << (bit % 8)
		err = bundle.VerifyBytes(both[:len(body)], both[len(body):], "k", keys)
		checkOnly(rt, err, bundle.ErrSignature, "bit %d changed: VerifyBytes", bit)
	})
}

// Loads share one keyring, and VerifyBytes only reads it: goroutines verifying
// against one keyring at once each get their call's own answer, and the
// keyring is unchanged afterwards. Under -race, a write to the map or to a
// pinned key by any of these calls is reported as a race.
func TestVerifyBytesSharesAKeyringAcrossGoroutines(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	good := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	keys := bundle.Keyring{"k1": k.pub, "weak": unhex(t, identityY), "short": k.pub[:ed25519.PublicKeySize-1]}
	pinned := cloneKeyring(keys)
	calls := []struct {
		id   string
		sig  []byte
		want error
	}{
		{"k1", good, nil},
		{"k1", make([]byte, ed25519.SignatureSize), bundle.ErrSignature},
		{"weak", good, bundle.ErrWeakKey},
		{"short", good, bundle.ErrKeySize},
		{"k2", good, bundle.ErrUnknownKey},
	}
	const workers, rounds = 8, 20
	got := make([][]error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			for range rounds {
				for _, c := range calls {
					got[w] = append(got[w], bundle.VerifyBytes([]byte(policyBody), c.sig, c.id, keys))
				}
			}
		})
	}
	wg.Wait()
	for w, errs := range got {
		for n, err := range errs {
			c := calls[n%len(calls)]
			if !checkOnly(t, err, c.want, "goroutine %d, call %d, id %q: VerifyBytes", w, n, c.id) {
				break
			}
		}
	}
	if !sameKeyring(keys, pinned) {
		t.Errorf("the keyring changed: %d ids pinned, were %d", len(keys), len(pinned))
	}
}
