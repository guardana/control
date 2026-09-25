// Package bundle signs and verifies the canonical bytes of a policy bundle:
// Ed25519 over DSSE's pre-authentication encoding, as ADR-0011 states it.
package bundle

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
)

const (
	// PayloadType is the DSSE payload type every policy bundle is signed under.
	// It carries no product name, for ADR-0010's reason.
	PayloadType = "application/vnd.agent-policy+json"

	// SignatureAlg is the only value PolicyBundle.signature_alg may hold. It
	// selects nothing.
	SignatureAlg = "ed25519-dsse"
)

// Keyring holds the public keys an operator pinned, by key id. The id only
// selects a key; it is never trusted on its own.
type Keyring map[string]ed25519.PublicKey

// Error is a refusal by SignBytes or VerifyBytes. A caller tells one from
// another with errors.Is, never by the text. The refusals are constants
// because VerifyBytes returns them bare: a variable that other code in the
// binary set to nil would turn its refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// One sentinel per refusal.
const (
	// ErrKeySize refuses a private or public key whose length is not the one
	// Ed25519 defines. crypto/ed25519 panics on such a key, so it never gets
	// one.
	ErrKeySize Error = "bundle: key has the wrong size"

	// ErrKeyPair refuses a private key whose public half is not the one its
	// seed derives.
	ErrKeyPair Error = "bundle: private key's public half is not its seed's"

	// ErrUnknownKey refuses a key id that selects no pinned key.
	ErrUnknownKey Error = "bundle: key id selects no pinned key"

	// ErrWeakKey refuses a pinned key of small order, under which a signature
	// can be forged without any private key.
	ErrWeakKey Error = "bundle: pinned key is of small order"

	// ErrSignature refuses a signature that does not verify over the
	// pre-authentication encoding of the canonical bytes.
	ErrSignature Error = "bundle: signature does not verify"
)

// PAE is DSSE's pre-authentication encoding of a payload type and a body. Both
// lengths are counted in bytes, and the result never shares memory with body.
func PAE(payloadType string, body []byte) []byte {
	out := []byte("DSSEv1 ")
	out = strconv.AppendInt(out, int64(len(payloadType)), 10)
	out = append(out, ' ')
	out = append(out, payloadType...)
	out = append(out, ' ')
	out = strconv.AppendInt(out, int64(len(body)), 10)
	out = append(out, ' ')
	return append(out, body...)
}

// Digest returns "sha256:" and the lowercase hex of sha256 over canonical.
func Digest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SignBytes signs the pre-authentication encoding of canonical. It refuses a
// key of the wrong size, and a key whose public half is not its seed's.
func SignBytes(canonical []byte, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: a private key of %d bytes, want %d", ErrKeySize, len(key), ed25519.PrivateKeySize)
	}
	// The library signs with whatever public half it is handed. Nothing
	// verifies such a signature, and two signatures of one message under one
	// seed with different halves give away the private scalar. The key that
	// signs is the derived one, which the caller cannot change after the
	// comparison.
	derived := ed25519.NewKeyFromSeed(key.Seed())
	if subtle.ConstantTimeCompare(derived, key) != 1 {
		return nil, ErrKeyPair
	}
	return ed25519.Sign(derived, PAE(PayloadType, canonical)), nil
}

// VerifyBytes verifies a signature over the pre-authentication encoding of
// canonical with the key keyID selects. It refuses an unknown id, a key of the
// wrong size and a key of small order. An empty id selects nothing, even from
// a keyring holding a key under it, because proto3 cannot tell an empty key_id
// from an absent one. A pinned key of the right size that is not a point on
// the curve is refused as ErrSignature: the library refuses it inside Verify
// without saying which of the two it refused.
func VerifyBytes(canonical, signature []byte, keyID string, keys Keyring) error {
	key, ok := keys[keyID]
	if !ok || keyID == "" {
		return ErrUnknownKey
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: a public key of %d bytes, want %d", ErrKeySize, len(key), ed25519.PublicKeySize)
	}
	// The small-order check and the verification read one copy, so a write to
	// the pinned key between them cannot show each a different key.
	var pub [ed25519.PublicKeySize]byte
	copy(pub[:], key)
	if smallOrder(pub) {
		return ErrWeakKey
	}
	if !ed25519.Verify(pub[:], PAE(PayloadType, canonical), signature) {
		return ErrSignature
	}
	return nil
}

// smallOrder reports whether y encodes a point of order 1, 2, 4 or 8. The
// library accepts such a key, and under it the signature R = identity, S = 0
// verifies for at least one message in eight, and for every message under the
// identity. The library reads y modulo p and the top bit as the sign of x, so
// these seven values, compared with that bit cleared, are every encoding of
// those points it accepts.
func smallOrder(y [ed25519.PublicKeySize]byte) bool {
	y[len(y)-1] &= 0x7f
	switch hex.EncodeToString(y[:]) {
	case "0000000000000000000000000000000000000000000000000000000000000000", // y = 0, order 4
		"0100000000000000000000000000000000000000000000000000000000000000", // y = 1, the identity
		"ecffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // y = p-1, order 2
		"edffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // y = p, read as 0
		"eeffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff7f", // y = p+1, read as 1
		"26e8958fc2b227b045c3f489f2ef98f0d5dfac05d3c63339b13802886d53fc05", // order 8
		"c7176a703d4dd84fba3c0b760d10670f2a2053fa2c39ccc64ec7fd7792ac037a": // order 8
		return true
	}
	return false
}
