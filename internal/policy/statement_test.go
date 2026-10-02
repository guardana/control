package policy_test

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
)

// What the statement tests check against, typed from ADR-0038.
const (
	statementType = "application/vnd.agent-policy-freshness+json"

	// statementBody names the example bundle, issued at 12:00:00 on the day
	// loadTime reads.
	statementBody = `{"kind":"agent-policy-freshness/v1alpha1","bundleId":"payments","serial":7,` +
		`"digest":"sha256:040d29038eaa67e07d4e99953eaad6456f25bebe0ad05a15412ec259e4717e15","issuedAt":"2026-09-11T12:00:00Z"}`
)

// freshKeys pins the freshness key, seed 0x03, under "f1": not a bundle key.
func freshKeys() bundle.Keyring { return bundle.Keyring{"f1": publicOf(3)} }

// signedBy is an envelope of the statement type holding body, signed by the
// library over the statement encoding written in this package's tests.
func signedBy(body string, signer byte, keyID string) policy.StatementEnvelope {
	sig := ed25519.Sign(key(signer), paeOf(statementType, []byte(body)))
	return policy.StatementEnvelope{
		PayloadType: statementType,
		Payload:     []byte(body),
		Signatures:  []policy.StatementSignature{{KeyID: keyID, Signature: sig}},
	}
}

// signed is body signed by the freshness key.
func signed(body string) policy.StatementEnvelope { return signedBy(body, 3, "f1") }

// statementSentinels are VerifyStatement's refusals.
func statementSentinels() []error {
	return []error{
		policy.ErrStatementPayloadType, policy.ErrStatementSignatures, policy.ErrStatementTooLarge,
		policy.ErrStatementKey, policy.ErrStatementSignature, policy.ErrStatementJSON,
		policy.ErrStatementRepeatedMember, policy.ErrStatementKind, policy.ErrStatementUnknownMember,
		policy.ErrStatementMissingMember, policy.ErrStatementValue, policy.ErrStatementIssuedAt,
	}
}

// expectStatementRefusal fails unless err is want and none of the other
// refusals of VerifyStatement.
func expectStatementRefusal(t tb, what string, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted, want %q", what, want)
	}
	for _, s := range statementSentinels() {
		if got, wanted := errors.Is(err, s), errors.Is(s, want); got != wanted {
			t.Errorf("%s: errors.Is(%q, %q) = %v, want %v", what, err, s, got, wanted)
		}
	}
}

func TestVerifyStatementReadsTheSignedBody(t *testing.T) {
	t.Parallel()
	st, err := policy.VerifyStatement(signed(statementBody), freshKeys())
	if err != nil {
		t.Fatalf("VerifyStatement: %v", err)
	}
	want := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	if st.BundleID() != "payments" || st.Serial() != 7 || st.Digest() != exampleDigest || !st.IssuedAt().Equal(want) {
		t.Fatalf("statement %q %d %s %v; want payments 7 %s %v", st.BundleID(), st.Serial(), st.Digest(), st.IssuedAt(), exampleDigest, want)
	}
	if st.IssuedAt().Location() != time.UTC {
		t.Errorf("issuedAt in %v, want UTC", st.IssuedAt().Location())
	}
}

// The envelope's checks: the payload type compared with the statement's, one
// signature, the key the keyid selects, a signature over the statement
// encoding of the fixed type.
func TestVerifyStatementRefusesABadEnvelope(t *testing.T) {
	t.Parallel()
	body := []byte(statementBody)
	overStatement := ed25519.Sign(key(3), paeOf(statementType, body))
	overBundle := ed25519.Sign(key(3), paeOf(payloadType, body))
	sig := func(keyID string, s []byte) []policy.StatementSignature {
		return []policy.StatementSignature{{KeyID: keyID, Signature: s}}
	}
	cases := []struct {
		name string
		env  policy.StatementEnvelope
		want error
	}{
		{"the bundle type, signed over the statement encoding", policy.StatementEnvelope{PayloadType: payloadType, Payload: body, Signatures: sig("f1", overStatement)}, policy.ErrStatementPayloadType},
		{"the bundle type, signed over its own encoding", policy.StatementEnvelope{PayloadType: payloadType, Payload: body, Signatures: sig("f1", overBundle)}, policy.ErrStatementPayloadType},
		{"no payload type", policy.StatementEnvelope{Payload: body, Signatures: sig("f1", overStatement)}, policy.ErrStatementPayloadType},
		{"the type one byte longer", policy.StatementEnvelope{PayloadType: statementType + " ", Payload: body, Signatures: sig("f1", overStatement)}, policy.ErrStatementPayloadType},
		{"no signature", policy.StatementEnvelope{PayloadType: statementType, Payload: body}, policy.ErrStatementSignatures},
		{"two good signatures", policy.StatementEnvelope{PayloadType: statementType, Payload: body,
			Signatures: append(sig("f1", overStatement), sig("f1", overStatement)...)}, policy.ErrStatementSignatures},
		{"a keyid nothing pins", policy.StatementEnvelope{PayloadType: statementType, Payload: body, Signatures: sig("f2", overStatement)}, policy.ErrStatementKey},
		{"an empty keyid", policy.StatementEnvelope{PayloadType: statementType, Payload: body, Signatures: sig("", overStatement)}, policy.ErrStatementKey},
		{"a bundle key's id", policy.StatementEnvelope{PayloadType: statementType, Payload: body, Signatures: sig("k1", overStatement)}, policy.ErrStatementKey},
		{"signed by a bundle key", signedBy(statementBody, 1, "f1"), policy.ErrStatementSignature},
		{"signed over the bundle encoding", policy.StatementEnvelope{PayloadType: statementType, Payload: body, Signatures: sig("f1", overBundle)}, policy.ErrStatementSignature},
		{"signed over the bare body", policy.StatementEnvelope{PayloadType: statementType, Payload: body, Signatures: sig("f1", ed25519.Sign(key(3), body))}, policy.ErrStatementSignature},
		{"a flipped signature bit", policy.StatementEnvelope{PayloadType: statementType, Payload: body, Signatures: sig("f1", flipFirstBit(overStatement))}, policy.ErrStatementSignature},
	}
	for _, c := range cases {
		st, err := policy.VerifyStatement(c.env, freshKeys())
		expectStatementRefusal(t, c.name, err, c.want)
		if st != (policy.Statement{}) {
			t.Errorf("%s: a statement beside the refusal", c.name)
		}
	}
	changed := signed(statementBody)
	changed.Payload = []byte(strings.Replace(statementBody, `"serial":7`, `"serial":8`, 1))
	_, err := policy.VerifyStatement(changed, freshKeys())
	expectStatementRefusal(t, "a body changed after signing", err, policy.ErrStatementSignature)

	weak := freshKeys()
	weak["f1"] = make(ed25519.PublicKey, ed25519.PublicKeySize)
	_, err = policy.VerifyStatement(signed(statementBody), weak)
	expectStatementRefusal(t, "a pinned key of small order", err, policy.ErrStatementKey)
	if !errors.Is(err, bundle.ErrWeakKey) {
		t.Errorf("the refusal %q does not wrap the bundle package's", err)
	}
}

// A bundle's signature never verifies as a statement's, and a statement's
// never as a bundle's, under one key pinned on both sides.
func TestStatementAndBundleSignaturesDoNotCross(t *testing.T) {
	t.Parallel()
	b := valid().build()
	asStatement := policy.StatementEnvelope{
		PayloadType: statementType,
		Payload:     b.GetCanonical(),
		Signatures:  []policy.StatementSignature{{KeyID: "k1", Signature: b.GetSignature()}},
	}
	_, err := policy.VerifyStatement(asStatement, pinned())
	expectStatementRefusal(t, "a bundle's signature as a statement's", err, policy.ErrStatementSignature)

	st := signedBy(statementBody, 1, "k1")
	if _, err := policy.VerifyStatement(st, pinned()); err != nil {
		t.Fatalf("the statement under the bundle key: %v", err)
	}
	asBundle := valid()
	asBundle.over = paeOf(statementType, []byte(exampleCanonical))
	_, err = policy.Load(asBundle.build(), pinned(), loadTime())
	expectOnly(t, err, policy.ErrSignature)
}

// Every body ADR-0038 refuses, each with its own refusal, signed so only the
// body's checks can refuse it.
func TestVerifyStatementRefusesABadBody(t *testing.T) {
	t.Parallel()
	for _, c := range badBodies() {
		_, err := policy.VerifyStatement(signed(c.body), freshKeys())
		expectStatementRefusal(t, c.name, err, c.want)
	}
}

type badBody struct {
	name, body string
	want       error
}

// edit replaces one exact piece of statementBody.
// TestEveryBadBodyIsAnEdit holds that each piece was there.
func edit(old, replacement string) string {
	return strings.Replace(statementBody, old, replacement, 1)
}

func TestEveryBadBodyIsAnEdit(t *testing.T) {
	t.Parallel()
	for _, c := range badBodies() {
		if c.body == statementBody {
			t.Errorf("%s: the body is the valid statement; the edit found nothing to replace", c.name)
		}
	}
}

const (
	kindMember     = `"kind":"agent-policy-freshness/v1alpha1",`
	bundleIDMember = `"bundleId":"payments",`
	serialMember   = `"serial":7,`
	digestMember   = `"digest":"sha256:040d29038eaa67e07d4e99953eaad6456f25bebe0ad05a15412ec259e4717e15",`
	issuedAtValue  = `"issuedAt":"2026-09-11T12:00:00Z"`
)

func badBodies() []badBody {
	cases := []badBody{
		{"another kind", edit("v1alpha1", "v1alpha2"), policy.ErrStatementKind},
		{"the policy document's kind", edit("agent-policy-freshness/v1alpha1", "agent-policy/v1alpha1"), policy.ErrStatementKind},
		{"a kind that is a number", edit(`"agent-policy-freshness/v1alpha1"`, `1`), policy.ErrStatementKind},
		{"an unknown member", edit(`{`, `{"expiresAt":"2026-09-11T12:05:00Z",`), policy.ErrStatementUnknownMember},
		{"a member in another case", edit(`"bundleId"`, `"BundleId"`), policy.ErrStatementUnknownMember},
		{"issuedAt twice, the later one newer", edit(issuedAtValue, `"issuedAt":"2026-09-11T11:00:00Z",`+`"issuedAt":"2026-09-11T13:00:00Z"`), policy.ErrStatementRepeatedMember},
		{"serial twice", edit(serialMember, `"serial":6,"serial":7,`), policy.ErrStatementRepeatedMember},
		{"bundleId twice, the same value", edit(bundleIDMember, bundleIDMember+bundleIDMember), policy.ErrStatementRepeatedMember},
		{"serial twice, once escaped", edit(serialMember, `"serial":6,"\u0073erial":7,`), policy.ErrStatementRepeatedMember},
		{"an unknown member twice", edit(`{`, `{"x":1,"x":1,`), policy.ErrStatementRepeatedMember},
		{"no kind", edit(kindMember, ``), policy.ErrStatementMissingMember},
		{"no bundleId", edit(bundleIDMember, ``), policy.ErrStatementMissingMember},
		{"no serial", edit(serialMember, ``), policy.ErrStatementMissingMember},
		{"no digest", edit(digestMember, ``), policy.ErrStatementMissingMember},
		{"no issuedAt", edit(`,`+issuedAtValue, ``), policy.ErrStatementMissingMember},
		{"an empty bundleId", edit(`"payments"`, `""`), policy.ErrStatementValue},
		{"a bundleId with a space at its end", edit(`"payments"`, `"payments "`), policy.ErrStatementValue},
		{"a null bundleId", edit(`"payments"`, `null`), policy.ErrStatementValue},
		{"a bundleId that is an object", edit(`"payments"`, `{}`), policy.ErrStatementValue},
		{"a serial in quotes", edit(`"serial":7`, `"serial":"7"`), policy.ErrStatementValue},
		{"a serial with a fraction", edit(`"serial":7`, `"serial":7.0`), policy.ErrStatementValue},
		{"a serial with an exponent", edit(`"serial":7`, `"serial":7e0`), policy.ErrStatementValue},
		{"a serial of 0", edit(`"serial":7`, `"serial":0`), policy.ErrStatementValue},
		{"a negative serial", edit(`"serial":7`, `"serial":-7`), policy.ErrStatementValue},
		{"a serial past the JSON-safe range", edit(`"serial":7`, `"serial":9007199254740992`), policy.ErrStatementJSON},
		{"a digest in upper case", edit("sha256:040d", "sha256:040D"), policy.ErrStatementValue},
		{"a digest one hex digit short", edit("17e15", "17e1"), policy.ErrStatementValue},
		{"a digest of another hash", edit("sha256:", "sha512:"), policy.ErrStatementValue},
		{"a digest without its prefix", edit("sha256:", ""), policy.ErrStatementValue},
		{"an array", `[]`, policy.ErrStatementJSON},
		{"a string", `"x"`, policy.ErrStatementJSON},
		{"nothing", ``, policy.ErrStatementJSON},
		{"a second object after it", statementBody + `{}`, policy.ErrStatementJSON},
		{"a word after it", statementBody + ` x`, policy.ErrStatementJSON},
		{"an unclosed object", strings.TrimSuffix(statementBody, "}"), policy.ErrStatementJSON},
		{"invalid UTF-8 in a value", edit(`"payments"`, "\"pay\xffments\""), policy.ErrStatementJSON},
		{"an unpaired surrogate in a value", edit(`"payments"`, `"pay\ud800ments"`), policy.ErrStatementJSON},
	}
	for _, spelling := range []string{
		"2026-09-11T12:00:00.000Z",
		"2026-09-11T12:00:00.5Z",
		"2026-09-11T12:00:00+00:00",
		"2026-09-11T12:00:00",
		"2026-09-11T12:00Z",
		"2026-09-11t12:00:00z",
		"2026-09-11 12:00:00Z",
		"2026-09-11T12:00:00z",
		"2026-9-11T12:00:00Z",
		" 2026-09-11T12:00:00Z",
		"2026-09-11T24:00:00Z",
		"2026-09-11T12:00:60Z",
		"2026-02-30T12:00:00Z",
		"+2026-09-11T12:00:00Z",
		"20260911T120000Z",
		"0001-01-01T00:00:00Z",
		"0001-01-01T00:00:01Z",
		"0000-01-01T00:00:00Z",
		"1969-12-31T23:59:59Z",
		"",
	} {
		cases = append(cases, badBody{"issuedAt " + spelling, edit("2026-09-11T12:00:00Z", spelling), policy.ErrStatementIssuedAt})
	}
	return append(cases, badBody{"an issuedAt that is a number", edit(`"2026-09-11T12:00:00Z"`, `1789128000`), policy.ErrStatementIssuedAt})
}

// The body's bound is exactly MaxStatementBytes: white space the format allows
// pads the statement to the bound, which is read, and one byte past it, which
// is refused before the signature is checked.
func TestStatementBodyBound(t *testing.T) {
	t.Parallel()
	if policy.MaxStatementBytes != 4096 {
		t.Fatalf("MaxStatementBytes = %d, want 4096 (ADR-0038)", policy.MaxStatementBytes)
	}
	at := statementBody + strings.Repeat(" ", 4096-len(statementBody))
	if _, err := policy.VerifyStatement(signed(at), freshKeys()); err != nil {
		t.Fatalf("a body of 4096 bytes: %v", err)
	}
	over := at + " "
	_, err := policy.VerifyStatement(signed(over), freshKeys())
	expectStatementRefusal(t, "a body of 4097 bytes", err, policy.ErrStatementTooLarge)
	unsigned := signed(over)
	unsigned.Signatures[0].Signature = nil
	_, err = policy.VerifyStatement(unsigned, freshKeys())
	expectStatementRefusal(t, "an unsigned body of 4097 bytes", err, policy.ErrStatementTooLarge)
}

// The JSON-safe range's last integer is a serial; the first past it is not.
func TestStatementSerialRange(t *testing.T) {
	t.Parallel()
	st, err := policy.VerifyStatement(signed(edit(`"serial":7`, `"serial":9007199254740991`)), freshKeys())
	if err != nil || st.Serial() != 9007199254740991 {
		t.Fatalf("serial 2^53-1: %d, %v", st.Serial(), err)
	}
	st, err = policy.VerifyStatement(signed(edit(`"serial":7`, `"serial":1`)), freshKeys())
	if err != nil || st.Serial() != 1 {
		t.Fatalf("serial 1: %d, %v", st.Serial(), err)
	}
}

// Members in any order, and white space between them, are one statement.
func TestStatementMembersInAnyOrder(t *testing.T) {
	t.Parallel()
	body := "{\n  " + issuedAtValue + ",\n  " + strings.TrimSuffix(digestMember, ",") + ",\n  " + strings.TrimSuffix(serialMember, ",") +
		",\n  " + strings.TrimSuffix(bundleIDMember, ",") + ",\n  " + strings.TrimSuffix(kindMember, ",") + "\n}\n"
	st, err := policy.VerifyStatement(signed(body), freshKeys())
	if err != nil {
		t.Fatalf("VerifyStatement: %v", err)
	}
	if st.BundleID() != "payments" || st.Serial() != 7 || st.Digest() != exampleDigest {
		t.Fatalf("statement %q %d %s", st.BundleID(), st.Serial(), st.Digest())
	}
}

func TestParseAndFormatIssuedAt(t *testing.T) {
	t.Parallel()
	got, err := policy.ParseIssuedAt("2026-09-11T12:00:00Z")
	if want := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC); err != nil || !got.Equal(want) {
		t.Fatalf("ParseIssuedAt = %v, %v; want %v", got, err, want)
	}
	plus2 := time.FixedZone("plus2", 2*60*60)
	if s := policy.FormatIssuedAt(time.Date(2026, time.September, 11, 14, 0, 0, 0, plus2)); s != "2026-09-11T12:00:00Z" {
		t.Fatalf("FormatIssuedAt = %q, want 2026-09-11T12:00:00Z", s)
	}
	for _, s := range []string{"2026-09-11T12:00:00.1Z", "0001-01-01T00:00:00Z", "0001-01-01T00:00:01Z", "1969-12-31T23:59:59Z", "10000-01-01T00:00:00Z"} {
		if _, err := policy.ParseIssuedAt(s); !errors.Is(err, policy.ErrStatementIssuedAt) {
			t.Errorf("ParseIssuedAt(%q) = %v, want ErrStatementIssuedAt", s, err)
		}
	}
	for s, unix := range map[string]int64{"1970-01-01T00:00:00Z": 0, "9999-12-31T23:59:59Z": 253402300799} {
		got, err := policy.ParseIssuedAt(s)
		if err != nil || got.Unix() != unix {
			t.Errorf("ParseIssuedAt(%q) = %v (%d), %v; want Unix %d", s, got, got.Unix(), err, unix)
		}
	}
}

// SignStatement writes the canonical body and signs it under the statement
// type: the library verifies its signature over the encoding typed here, and
// VerifyStatement reads it back.
func TestSignStatementWritesWhatVerifyStatementReads(t *testing.T) {
	t.Parallel()
	issued := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	env, err := policy.SignStatement("payments", 7, exampleDigest, issued, key(3), "f1")
	if err != nil {
		t.Fatalf("SignStatement: %v", err)
	}
	const want = `{"bundleId":"payments","digest":"sha256:040d29038eaa67e07d4e99953eaad6456f25bebe0ad05a15412ec259e4717e15",` +
		`"issuedAt":"2026-09-11T12:00:00Z","kind":"agent-policy-freshness/v1alpha1","serial":7}`
	if string(env.Payload) != want || env.PayloadType != statementType || len(env.Signatures) != 1 || env.Signatures[0].KeyID != "f1" {
		t.Fatalf("SignStatement = %q %q %d signatures", env.PayloadType, env.Payload, len(env.Signatures))
	}
	if !ed25519.Verify(publicOf(3), paeOf(statementType, []byte(want)), env.Signatures[0].Signature) {
		t.Fatal("the signature does not verify over the statement encoding of the body")
	}
	st, err := policy.VerifyStatement(env, freshKeys())
	if err != nil || st.Serial() != 7 || !st.IssuedAt().Equal(issued) {
		t.Fatalf("VerifyStatement = %d %v, %v", st.Serial(), st.IssuedAt(), err)
	}
}

// SignStatement refuses what no plane would take, rather than round it.
func TestSignStatementRefusesWhatNoPlaneTakes(t *testing.T) {
	t.Parallel()
	issued := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		id       string
		serial   int64
		digest   string
		issuedAt time.Time
		key      ed25519.PrivateKey
		keyID    string
		want     error
	}{
		{"a fraction of a second", "payments", 7, exampleDigest, issued.Add(time.Nanosecond), key(3), "f1", policy.ErrStatementIssuedAt},
		{"the zero time", "payments", 7, exampleDigest, time.Time{}, key(3), "f1", policy.ErrStatementIssuedAt},
		{"an empty bundle id", "", 7, exampleDigest, issued, key(3), "f1", policy.ErrStatementValue},
		{"serial 0", "payments", 0, exampleDigest, issued, key(3), "f1", policy.ErrStatementValue},
		{"a malformed digest", "payments", 7, "sha256:00", issued, key(3), "f1", policy.ErrStatementValue},
		{"an empty key id", "payments", 7, exampleDigest, issued, key(3), "", policy.ErrKey},
		{"a short key", "payments", 7, exampleDigest, issued, key(3)[:63], "f1", policy.ErrSigningKey},
	}
	for _, c := range cases {
		env, err := policy.SignStatement(c.id, c.serial, c.digest, c.issuedAt, c.key, c.keyID)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: SignStatement = %v, want %q", c.name, err, c.want)
		}
		if env.Payload != nil || env.Signatures != nil {
			t.Errorf("%s: an envelope beside the refusal", c.name)
		}
	}
}
