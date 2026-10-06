package reaction_test

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/reaction"
)

// withBit returns a copy of pub with bit b of its last byte flipped. Bit 7 is
// the encoding's bit 255, the sign of x: flipped, the key is the negated
// point, which the same seed can sign for.
func withBit(pub ed25519.PublicKey, b uint) ed25519.PublicKey {
	out := bytes.Clone(pub)
	out[ed25519.PublicKeySize-1] ^= 1 << b
	return out
}

func TestDistinctKeysTreatsTheNegatedPointAsTheSameKey(t *testing.T) {
	a := pubOf(routeKey())
	expectOnly(t, "a key and its negation", reaction.DistinctKeys(a, withBit(a, 7)), reaction.ErrKeysEqual, envelopeRefusals())
	expectOnly(t, "a key's negation and the key", reaction.DistinctKeys(pubOf(liftKey()), withBit(a, 7), a), reaction.ErrKeysEqual, envelopeRefusals())
	if err := reaction.DistinctKeys(a, withBit(a, 6)); err != nil {
		t.Errorf("two keys apart in bit 254: %v", err)
	}
}

// routeLiftKeyed is the test route with lift as its lift key.
func routeLiftKeyed(t *testing.T, lift ed25519.PublicKey) reaction.Route {
	t.Helper()
	r, err := reaction.ParseRoute(routeNamingLiftKey(lift))
	if err != nil {
		t.Fatalf("ParseRoute naming lift key %x: %v", lift, err)
	}
	return r
}

func TestSignRouteRefusesTheNegatedLiftKey(t *testing.T) {
	negated := routeLiftKeyed(t, withBit(pubOf(routeKey()), 7))
	_, err := reaction.SignRoute(negated, routeKey())
	expectOnly(t, "a route key whose negation is the lift key", err, reaction.ErrKeysEqual, envelopeRefusals())
}

// VerifyRoute refuses a route whose lift key is the key it verifies under, or
// that key's negation, though the signature over it is good.
func TestVerifyRouteRefusesARouteSignedByItsLiftKey(t *testing.T) {
	key := routeKey()
	for _, c := range []struct {
		name string
		lift ed25519.PublicKey
	}{
		{"the route key", pubOf(key)},
		{"the route key's negation", withBit(pubOf(key), 7)},
	} {
		body := routeLiftKeyed(t, c.lift).Canonical()
		sig, err := bundle.SignBytesAs(reaction.RoutePayloadType, body, key)
		if err != nil {
			t.Fatal(err)
		}
		env := reaction.Envelope{PayloadType: reaction.RoutePayloadType, Payload: body,
			Signatures: []reaction.Signature{{KeyID: keyIDOf(key), Signature: sig}}}
		_, err = reaction.VerifyRoute(env, pubOf(key))
		expectOnly(t, "a lift key that is "+c.name, err, reaction.ErrKeysEqual, envelopeRefusals())
	}
}
