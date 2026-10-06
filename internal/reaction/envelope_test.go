package reaction_test

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/reaction"
)

func signedRoute(t testing.TB) reaction.Envelope {
	t.Helper()
	env, err := reaction.SignRoute(validRoute(t), routeKey())
	if err != nil {
		t.Fatalf("SignRoute: %v", err)
	}
	return env
}

func TestSignRouteVerifies(t *testing.T) {
	env := signedRoute(t)
	if env.PayloadType != "application/vnd.agent-reaction-route+json" || string(env.Payload) != withLift(routeCanonical) ||
		len(env.Signatures) != 1 || env.Signatures[0].KeyID != keyIDOf(routeKey()) {
		t.Fatalf("envelope = %q %s %+v", env.PayloadType, env.Payload, env.Signatures)
	}
	r, err := reaction.VerifyRoute(env, pubOf(routeKey()))
	if err != nil {
		t.Fatalf("VerifyRoute: %v", err)
	}
	if r.Digest() != validRoute(t).Digest() || r.ID() != "refunds" {
		t.Fatalf("verified route %q %s", r.ID(), r.Digest())
	}
}

func TestSignRouteRefuses(t *testing.T) {
	_, err := reaction.SignRoute(reaction.Route{}, routeKey())
	expectOnly(t, "the zero route", err, reaction.ErrRouteJSON, routeRefusals())
	_, err = reaction.SignRoute(validRoute(t), liftKey())
	expectOnly(t, "the route's own lift key", err, reaction.ErrKeysEqual, envelopeRefusals())
	_, err = reaction.SignRoute(validRoute(t), routeKey()[:40])
	expectOnly(t, "a key of 40 bytes", err, reaction.ErrSigningKey, envelopeRefusals())
	mismatched := append(bytes.Clone(routeKey()[:ed25519.SeedSize]), pubOf(liftKey())...)
	_, err = reaction.SignRoute(validRoute(t), mismatched)
	expectOnly(t, "a key whose public half is another's", err, reaction.ErrSigningKey, envelopeRefusals())
}

func TestVerifyRouteRefuses(t *testing.T) {
	good := signedRoute(t)
	with := func(f func(*reaction.Envelope)) reaction.Envelope {
		env := reaction.Envelope{PayloadType: good.PayloadType, Payload: bytes.Clone(good.Payload), Signatures: []reaction.Signature{good.Signatures[0]}}
		f(&env)
		return env
	}
	signedBody := func(body string) reaction.Envelope {
		sig, err := bundle.SignBytesAs(reaction.RoutePayloadType, []byte(body), routeKey())
		if err != nil {
			t.Fatal(err)
		}
		return reaction.Envelope{PayloadType: reaction.RoutePayloadType, Payload: []byte(body), Signatures: []reaction.Signature{{KeyID: keyIDOf(routeKey()), Signature: sig}}}
	}
	for _, c := range []struct {
		name string
		env  reaction.Envelope
		pub  ed25519.PublicKey
		want error
	}{
		{"the lift's payload type", with(func(e *reaction.Envelope) { e.PayloadType = reaction.LiftPayloadType }), pubOf(routeKey()), reaction.ErrRoutePayloadType},
		{"the bundle's payload type", with(func(e *reaction.Envelope) { e.PayloadType = bundle.PayloadType }), pubOf(routeKey()), reaction.ErrRoutePayloadType},
		{"no signature", with(func(e *reaction.Envelope) { e.Signatures = nil }), pubOf(routeKey()), reaction.ErrRouteSignatures},
		{"two signatures", with(func(e *reaction.Envelope) { e.Signatures = append(e.Signatures, e.Signatures[0]) }), pubOf(routeKey()), reaction.ErrRouteSignatures},
		{"a body one byte over", with(func(e *reaction.Envelope) { e.Payload = bytes.Repeat([]byte{' '}, 65537) }), pubOf(routeKey()), reaction.ErrRouteTooLarge},
		{"a body at the bound", with(func(e *reaction.Envelope) { e.Payload = bytes.Repeat([]byte{' '}, 65536) }), pubOf(routeKey()), reaction.ErrRouteSignature},
		{"another key", good, pubOf(liftKey()), reaction.ErrRouteKey},
		{"a key of 31 bytes", good, pubOf(routeKey())[:31], reaction.ErrRouteKey},
		{"a keyid of another key", with(func(e *reaction.Envelope) { e.Signatures[0].KeyID = keyIDOf(liftKey()) }), pubOf(routeKey()), reaction.ErrRouteKey},
		{"an empty keyid", with(func(e *reaction.Envelope) { e.Signatures[0].KeyID = "" }), pubOf(routeKey()), reaction.ErrRouteKey},
		{"a changed body", with(func(e *reaction.Envelope) {
			e.Payload = bytes.Replace(e.Payload, []byte(`"serial":7`), []byte(`"serial":8`), 1)
		}), pubOf(routeKey()), reaction.ErrRouteSignature},
		{"a changed signature", with(func(e *reaction.Envelope) {
			e.Signatures[0].Signature = bytes.Clone(e.Signatures[0].Signature)
			e.Signatures[0].Signature[0] ^= 1
		}), pubOf(routeKey()), reaction.ErrRouteSignature},
		{"a signed body that is not canonical", signedBody(withLift(routeTemplate)), pubOf(routeKey()), reaction.ErrRouteNotCanonical},
		{"a signed body that is no route", signedBody(`{"kind":"reaction-route/v1alpha1"}`), pubOf(routeKey()), reaction.ErrRouteMember},
	} {
		_, err := reaction.VerifyRoute(c.env, c.pub)
		expectOnly(t, c.name, err, c.want, append(envelopeRefusals(), reaction.ErrRouteMember))
	}
}

// The route returned shares no memory with the envelope: a write to the
// caller's payload after the call cannot change it.
func TestVerifyRouteKeepsItsOwnCopy(t *testing.T) {
	env := signedRoute(t)
	r, err := reaction.VerifyRoute(env, pubOf(routeKey()))
	if err != nil {
		t.Fatal(err)
	}
	copy(env.Payload, bytes.Repeat([]byte{'x'}, len(env.Payload)))
	if string(r.Canonical()) != withLift(routeCanonical) {
		t.Fatal("the route shares memory with the envelope it was read from")
	}
}

func TestRouteFileRoundTrips(t *testing.T) {
	env := signedRoute(t)
	raw, err := reaction.MarshalRouteFile(env)
	if err != nil {
		t.Fatalf("MarshalRouteFile: %v", err)
	}
	back, err := reaction.ParseRouteFile(raw)
	if err != nil {
		t.Fatalf("ParseRouteFile: %v", err)
	}
	if _, err := reaction.VerifyRoute(back, pubOf(routeKey())); err != nil {
		t.Fatalf("VerifyRoute of the file read back: %v", err)
	}
}

func TestParseRouteFileBoundAndRefusals(t *testing.T) {
	raw, err := reaction.MarshalRouteFile(signedRoute(t))
	if err != nil {
		t.Fatal(err)
	}
	pad := func(n int) []byte { return append(bytes.Clone(raw), bytes.Repeat([]byte{' '}, n-len(raw))...) }
	if _, err := reaction.ParseRouteFile(pad(131072)); err != nil {
		t.Fatalf("a file of 131072 bytes: %v", err)
	}
	refusals := []error{
		policykey.ErrEnvelopeFileTooLarge, policykey.ErrEnvelope, policykey.ErrEnvelopeMember, policykey.ErrEnvelopeRepeated, policykey.ErrEnvelopeBase64,
		policykey.ErrStatementFileTooLarge, policykey.ErrStatementEnvelope, policykey.ErrStatementEnvelopeMember,
		policykey.ErrStatementEnvelopeRepeated, policykey.ErrStatementBase64,
	}
	s := string(raw)
	for _, c := range []struct {
		name string
		raw  []byte
		want error
	}{
		{"a file one byte over", pad(131073), policykey.ErrEnvelopeFileTooLarge},
		{"an array", []byte(`[]`), policykey.ErrEnvelope},
		{"an unknown member", []byte(strings.Replace(s, `{"payloadType"`, `{"x":1,"payloadType"`, 1)), policykey.ErrEnvelopeMember},
		{"payloadType twice", []byte(strings.Replace(s, `{"payloadType"`, `{"payloadType":"a","payloadType"`, 1)), policykey.ErrEnvelopeRepeated},
		{"URL-safe base64", []byte(strings.Replace(s, `"payload":"`, `"payload":"-_`, 1)), policykey.ErrEnvelopeBase64},
	} {
		_, err := reaction.ParseRouteFile(c.raw)
		expectOnly(t, c.name, err, c.want, refusals)
	}
	_, err = reaction.MarshalRouteFile(reaction.Envelope{PayloadType: reaction.RoutePayloadType, Payload: make([]byte, 131072)})
	expectOnly(t, "marshalling a body over the file's bound", err, policykey.ErrEnvelopeFileTooLarge, refusals)
}

func TestDistinctKeys(t *testing.T) {
	a, b, c := pubOf(seededKey(1)), pubOf(seededKey(2)), pubOf(seededKey(3))
	if err := reaction.DistinctKeys(a, b, c); err != nil {
		t.Fatalf("three distinct keys: %v", err)
	}
	if err := reaction.DistinctKeys(); err != nil {
		t.Fatalf("no key: %v", err)
	}
	for _, k := range []struct {
		name string
		keys []ed25519.PublicKey
		want error
	}{
		{"the first and the last equal by bytes", []ed25519.PublicKey{a, b, c, bytes.Clone(a)}, reaction.ErrKeysEqual},
		{"two neighbours equal", []ed25519.PublicKey{a, b, bytes.Clone(b)}, reaction.ErrKeysEqual},
		{"a key of 31 bytes", []ed25519.PublicKey{a, b[:31]}, reaction.ErrKeySize},
		{"a nil key", []ed25519.PublicKey{a, nil}, reaction.ErrKeySize},
	} {
		expectOnly(t, k.name, reaction.DistinctKeys(k.keys...), k.want, envelopeRefusals())
	}
}

// crossSigned is one signer's body and signature, each made by the signer of
// its kind under one key.
type crossSigned struct {
	kind string
	body []byte
	sig  []byte
}

func crossSigners(t *testing.T, key ed25519.PrivateKey) []crossSigned {
	t.Helper()
	body := []byte(`{"kind":"agent-policy/v1alpha1"}`)
	bundleSig, err := bundle.SignBytes(body, key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := policy.SignStatement("b1", 1, procDigest, time.Unix(1_800_000_000, 0), key, keyIDOf(key))
	if err != nil {
		t.Fatal(err)
	}
	route, err := reaction.SignRoute(validRoute(t), key)
	if err != nil {
		t.Fatal(err)
	}
	lift, err := reaction.SignLift(validLift(), key)
	if err != nil {
		t.Fatal(err)
	}
	return []crossSigned{
		{"bundle", body, bundleSig},
		{"statement", st.Payload, st.Signatures[0].Signature},
		{"route", route.Payload, route.Signatures[0].Signature},
		{"lift", lift.Payload, lift.Signatures[0].Signature},
	}
}

// TestSignaturesNeverVerifyAsAnotherKind: under one key, a bundle's, a
// statement's, a route's and a lift's signature each verify by their own
// verifier only. Each verifier is handed the envelope labelled as its own
// kind, so only the signature can refuse.
func TestSignaturesNeverVerifyAsAnotherKind(t *testing.T) {
	key := routeKey()
	pub, id := pubOf(key), keyIDOf(key)
	ring := bundle.Keyring{id: pub}
	sigs := func(s []byte) []reaction.Signature { return []reaction.Signature{{KeyID: id, Signature: s}} }
	verifiers := []struct {
		kind      string
		signature error
		verify    func(body, sig []byte) error
	}{
		{"bundle", bundle.ErrSignature, func(body, sig []byte) error { return bundle.VerifyBytes(body, sig, id, ring) }},
		{"statement", policy.ErrStatementSignature, func(body, sig []byte) error {
			_, err := policy.VerifyStatement(policy.StatementEnvelope{PayloadType: bundle.StatementPayloadType, Payload: body, Signatures: sigs(sig)}, ring)
			return err
		}},
		{"route", reaction.ErrRouteSignature, func(body, sig []byte) error {
			_, err := reaction.VerifyRoute(reaction.Envelope{PayloadType: reaction.RoutePayloadType, Payload: body, Signatures: sigs(sig)}, pub)
			return err
		}},
		{"lift", reaction.ErrLiftSignature, func(body, sig []byte) error {
			_, err := reaction.VerifyLift(reaction.Envelope{PayloadType: reaction.LiftPayloadType, Payload: body, Signatures: sigs(sig)}, pub)
			return err
		}},
	}
	for _, s := range crossSigners(t, key) {
		for _, v := range verifiers {
			err := v.verify(s.body, s.sig)
			switch {
			case s.kind == v.kind && err != nil:
				t.Errorf("a %s signature does not verify as a %s: %v", s.kind, v.kind, err)
			case s.kind != v.kind && !errors.Is(err, v.signature):
				t.Errorf("a %s signature verified as a %s gives %v, want %q", s.kind, v.kind, err, v.signature)
			}
		}
	}
}
