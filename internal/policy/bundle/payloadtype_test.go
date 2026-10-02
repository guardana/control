package bundle_test

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
)

// The statement type of ADR-0038 and a body's encoding under it, the length
// counted by hand.
const (
	statementType       = "application/vnd.agent-policy-freshness+json"
	signedStatementBody = `DSSEv1 43 application/vnd.agent-policy-freshness+json 7 {"a":1}`
)

func TestStatementPayloadTypeIsTheRecordsValue(t *testing.T) {
	if bundle.StatementPayloadType != statementType {
		t.Fatalf("StatementPayloadType = %q, want %q", bundle.StatementPayloadType, statementType)
	}
	if got := bundle.PAE(bundle.StatementPayloadType, []byte(policyBody)); string(got) != signedStatementBody {
		t.Fatalf("PAE = %q, want %q", got, signedStatementBody)
	}
}

// SignBytesAs signs the encoding of the type it is given, which the library's
// own signature over the typed bytes pins, and VerifyBytesAs accepts it under
// that type.
func TestSignBytesAsSignsTheEncodingOfTheGivenType(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	keys := bundle.Keyring{"k1": k.pub}
	for _, tc := range []struct{ payloadType, signed string }{
		{statementType, signedStatementBody},
		{"application/vnd.agent-policy+json", signedPolicyBody},
	} {
		sig, err := bundle.SignBytesAs(tc.payloadType, []byte(policyBody), k.priv)
		if err != nil {
			t.Fatalf("SignBytesAs(%q): %v", tc.payloadType, err)
		}
		if want := ed25519.Sign(k.priv, []byte(tc.signed)); !bytes.Equal(sig, want) {
			t.Errorf("SignBytesAs(%q) = %x, want the library's signature over %q", tc.payloadType, sig, tc.signed)
		}
		checkOnly(t, bundle.VerifyBytesAs(tc.payloadType, []byte(policyBody), sig, "k1", keys), nil, "VerifyBytesAs(%q)", tc.payloadType)
	}
}

// A bundle's signature never verifies as a statement's, and a statement's
// never as a bundle's: the signatures are the library's own over the typed
// encodings, so neither direction depends on SignBytesAs.
func TestASignatureVerifiesUnderItsOwnPayloadTypeOnly(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	keys := bundle.Keyring{"k1": k.pub}
	body := []byte(policyBody)
	bundleSig := ed25519.Sign(k.priv, []byte(signedPolicyBody))
	statementSig := ed25519.Sign(k.priv, []byte(signedStatementBody))

	checkOnly(t, bundle.VerifyBytesAs(statementType, body, bundleSig, "k1", keys), bundle.ErrSignature, "a bundle signature as a statement's")
	checkOnly(t, bundle.VerifyBytes(body, statementSig, "k1", keys), bundle.ErrSignature, "a statement signature as a bundle's")
	checkOnly(t, bundle.VerifyBytesAs("application/vnd.agent-policy+json", body, statementSig, "k1", keys), bundle.ErrSignature,
		"a statement signature under the bundle type")
	checkOnly(t, bundle.VerifyBytesAs(statementType, body, statementSig, "k1", keys), nil, "the statement signature")
	checkOnly(t, bundle.VerifyBytes(body, bundleSig, "k1", keys), nil, "the bundle signature")
}

// VerifyBytesAs makes every check VerifyBytes makes: the id selects, the key
// has the right size and is not of small order.
func TestVerifyBytesAsRefusesWhatVerifyBytesRefuses(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	sig := ed25519.Sign(k.priv, []byte(signedStatementBody))
	body := []byte(policyBody)
	checkOnly(t, bundle.VerifyBytesAs(statementType, body, sig, "", bundle.Keyring{"": k.pub}), bundle.ErrUnknownKey, "an empty id")
	checkOnly(t, bundle.VerifyBytesAs(statementType, body, sig, "k2", bundle.Keyring{"k1": k.pub}), bundle.ErrUnknownKey, "an unpinned id")
	checkOnly(t, bundle.VerifyBytesAs(statementType, body, sig, "k1", bundle.Keyring{"k1": k.pub[:31]}), bundle.ErrKeySize, "a short key")
	checkOnly(t, bundle.VerifyBytesAs(statementType, body, sig, "k1", bundle.Keyring{"k1": unhex(t, identityY)}), bundle.ErrWeakKey, "a key of small order")
}

// SignBytesAs refuses what SignBytes refuses.
func TestSignBytesAsRefusesWhatSignBytesRefuses(t *testing.T) {
	k := rfcKey(t, rfc1Seed, rfc1Pub)
	_, err := bundle.SignBytesAs(statementType, []byte(policyBody), k.priv[:63])
	checkOnly(t, err, bundle.ErrKeySize, "a short private key")
	other := rfcKey(t, rfc2Seed, rfc2Pub)
	mixed := append(bytes.Clone(k.priv[:ed25519.SeedSize]), other.pub...)
	_, err = bundle.SignBytesAs(statementType, []byte(policyBody), mixed)
	checkOnly(t, err, bundle.ErrKeyPair, "a private key with another public half")
}
