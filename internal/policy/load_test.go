package policy_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/rules"
)

// TestTheTestsBuildWhatTheGoldensSay holds this package's own encoding, digest
// and key to the values computed outside Go, so that no refusal below passes
// because a test built its bundle wrong.
func TestTheTestsBuildWhatTheGoldensSay(t *testing.T) {
	t.Parallel()
	if got := hex.EncodeToString(valid().build().GetSignature()); got != exampleSignature {
		t.Errorf("the tests' own signature = %s, want %s", got, exampleSignature)
	}
	if got := digestOf([]byte(exampleCanonical)); got != exampleDigest {
		t.Errorf("the tests' own digest = %s, want %s", got, exampleDigest)
	}
	if got := hex.EncodeToString(publicOf(1)); got != seed01Public {
		t.Errorf("seed 0x01 derives %s, want %s", got, seed01Public)
	}
}

// TestSentinelsAreDistinct: the refusals are constants of one string type, so
// two with the same text would be one value, and errors.Is could not tell
// their checks apart.
func TestSentinelsAreDistinct(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, s := range sentinels() {
		if seen[s.Error()] {
			t.Errorf("two refusals read %q", s)
		}
		seen[s.Error()] = true
	}
}

// TestLoadAcceptsTheExample is the accepting twin of every refusal below:
// valid() builds it, and each refusal changes it in one place.
func TestLoadAcceptsTheExample(t *testing.T) {
	t.Parallel()
	ref := load(t, valid().build()).Ref()
	want := &controlv1.PolicyBundleRef{BundleId: "payments", Version: "2026-09-10.1", Digest: exampleDigest}
	if !proto.Equal(ref, want) {
		t.Fatalf("Ref() = %v, want %v", ref, want)
	}
}

// change is one edit of a signed bundle and the check that must refuse it.
type change struct {
	// field is the field of PolicyBundle the row changes, or "ref." and a field
	// of PolicyBundleRef, or "unknown".
	field  string
	name   string
	edit   func(draft) draft
	want   error
	within error // the bundle package's refusal the check wraps, if it wraps one
}

func setAlg(alg string) func(draft) draft {
	return func(d draft) draft { d.alg = alg; return d }
}

func setKeyID(id string) func(draft) draft {
	return func(d draft) draft { d.keyID = id; return d }
}

func setSignature(edit func([]byte) []byte) func(draft) draft {
	return func(d draft) draft { d.editSig = edit; return d }
}

func signOver(over func(doc []byte) []byte) func(draft) draft {
	return func(d draft) draft { d.over = over(d.doc); return d }
}

func setDoc(doc string) func(draft) draft {
	return func(d draft) draft { return d.withDoc(doc) }
}

// tampered carries after while its signature covers before, and names after
// by its own digest, so that only the signature can tell.
func tampered(before, after string) func(draft) draft {
	return func(d draft) draft {
		d = d.withDoc(after)
		d.over = pae([]byte(before))
		return d
	}
}

func setDigest(digest string) func(draft) draft {
	return func(d draft) draft { d.digest = digest; return d }
}

// reorderedExample is the example with two members of its bundle swapped: the
// same bytes in another order, a valid document of the same length, and not
// its own canonical form.
func reorderedExample() string {
	return strings.Replace(exampleCanonical, `"id":"payments","maxStaleSeconds":300`, `"maxStaleSeconds":300,"id":"payments"`, 1)
}

// lastHexDigitOff changes only the last character of a digest, so that a
// comparison that stops early takes it for the true one.
func lastHexDigitOff(digest string) string {
	last := digest[len(digest)-1]
	if last == '5' {
		last = '6'
	} else {
		last = '5'
	}
	return digest[:len(digest)-1] + string(last)
}

func setRef(bundleID, version string) func(draft) draft {
	return func(d draft) draft { d.bundleID, d.version = bundleID, version; return d }
}

func setMaxStale(seconds int64) func(draft) draft {
	return func(d draft) draft { d.maxStale = seconds; return d }
}

func setCreatedAt(ts *timestamppb.Timestamp) func(draft) draft {
	return func(d draft) draft { d.createdAt = ts; return d }
}

func withUnknown(where string) func(draft) draft {
	return func(d draft) draft { d.unknownIn = []string{where}; return d }
}

// changes edit every field of the example's bundle, and the fields a sender
// can add, each alone.
func changes() []change {
	exampleHex := strings.TrimPrefix(exampleDigest, "sha256:")
	return []change{
		{"unknown", "a field no message declares, on the bundle", withUnknown(inBundle), policy.ErrUnknownField, nil},
		{"unknown", "a field no message declares, on the ref", withUnknown(inRef), policy.ErrUnknownField, nil},
		{"unknown", "a field no message declares, on ref.created_at", withUnknown(inCreatedAt), policy.ErrUnknownField, nil},

		{"signature_alg", "none", setAlg(""), policy.ErrSignatureAlg, nil},
		{"signature_alg", "ed25519", setAlg("ed25519"), policy.ErrSignatureAlg, nil},
		{"signature_alg", "in capitals", setAlg("ED25519-DSSE"), policy.ErrSignatureAlg, nil},
		{"signature_alg", "with a trailing space", setAlg("ed25519-dsse "), policy.ErrSignatureAlg, nil},

		{"key_id", "one nobody pinned", setKeyID("k3"), policy.ErrKey, bundle.ErrUnknownKey},
		{"key_id", "none", setKeyID(""), policy.ErrKey, bundle.ErrUnknownKey},
		{"key_id", "a pinned id in capitals", setKeyID("K1"), policy.ErrKey, bundle.ErrUnknownKey},

		{"signature", "one bit flipped", setSignature(flipFirstBit), policy.ErrSignature, bundle.ErrSignature},
		{"signature", "none", setSignature(func([]byte) []byte { return nil }), policy.ErrSignature, bundle.ErrSignature},
		{"signature", "cut short", setSignature(func(s []byte) []byte { return s[:63] }), policy.ErrSignature, bundle.ErrSignature},
		{"signature", "by the other pinned key", func(d draft) draft { d.signer = key(2); return d }, policy.ErrSignature, bundle.ErrSignature},
		{"signature", "over the bare canonical bytes", signOver(slices.Clone), policy.ErrSignature, bundle.ErrSignature},
		{"signature", "under another payload type", signOver(func(doc []byte) []byte { return paeOf("application/json", doc) }), policy.ErrSignature, bundle.ErrSignature},

		{"canonical", "changed after signing", tampered(exampleCanonical, strings.Replace(exampleCanonical, "10000", "99999", 1)), policy.ErrSignature, bundle.ErrSignature},
		{"canonical", "not JSON, signed", setDoc("not json"), policy.ErrDocument, nil},
		{"canonical", "JSON that is no policy, signed", setDoc(`{}`), policy.ErrDocument, nil},
		{"canonical", "empty, signed", setDoc(""), policy.ErrDocument, nil},
		{"canonical", "a policy the format refuses, signed", setDoc(strings.Replace(exampleCanonical, `"serial":7`, `"serial":0`, 1)), policy.ErrDocument, nil},
		{"canonical", "in its author's spelling, signed", setDoc(exampleRaw), policy.ErrNotCanonical, nil},
		{"canonical", "with a trailing line feed, signed", setDoc(exampleCanonical + "\n"), policy.ErrNotCanonical, nil},
		{"canonical", "with two keys swapped, same length, signed", setDoc(reorderedExample()), policy.ErrNotCanonical, nil},

		{"ref", "none at all", func(d draft) draft { d.edit = func(b *controlv1.PolicyBundle) { b.Ref = nil }; return d }, policy.ErrDigest, nil},
		{"ref.digest", "of other bytes", setDigest(digestOf([]byte("other"))), policy.ErrDigest, nil},
		{"ref.digest", "of the signed encoding rather than the document", setDigest(digestOf(pae([]byte(exampleCanonical)))), policy.ErrDigest, nil},
		{"ref.digest", "in capitals", setDigest("sha256:" + strings.ToUpper(exampleHex)), policy.ErrDigest, nil},
		{"ref.digest", "without its prefix", setDigest(exampleHex), policy.ErrDigest, nil},
		{"ref.digest", "none", setDigest(""), policy.ErrDigest, nil},
		{"ref.digest", "one hex digit off, the last", setDigest(lastHexDigitOff(exampleDigest)), policy.ErrDigest, nil},

		{"ref.bundle_id", "another bundle's", setRef("refunds", "2026-09-10.1"), policy.ErrMismatch, nil},
		{"ref.bundle_id", "in other case", setRef("Payments", "2026-09-10.1"), policy.ErrMismatch, nil},
		{"ref.bundle_id", "none", setRef("", "2026-09-10.1"), policy.ErrMismatch, nil},
		{"ref.version", "another version", setRef("payments", "2026-09-10.2"), policy.ErrMismatch, nil},
		{"ref.version", "none", setRef("payments", ""), policy.ErrMismatch, nil},
		{"max_stale_seconds", "one above the document's", setMaxStale(301), policy.ErrMismatch, nil},
		{"max_stale_seconds", "one below the document's", setMaxStale(299), policy.ErrMismatch, nil},
		{"max_stale_seconds", "none", setMaxStale(0), policy.ErrMismatch, nil},
		{"max_stale_seconds", "negative", setMaxStale(-300), policy.ErrMismatch, nil},

		{"ref.created_at", "set", setCreatedAt(timestamppb.New(loadTime())), policy.ErrCreatedAt, nil},
		{"ref.created_at", "set to its zero value", setCreatedAt(&timestamppb.Timestamp{}), policy.ErrCreatedAt, nil},
	}
}

// TestLoadRefusesEveryChange changes a signed bundle in one place at a time
// and names the check that must refuse it, and no other.
func TestLoadRefusesEveryChange(t *testing.T) {
	t.Parallel()
	for _, c := range changes() {
		t.Run(c.field+"/"+c.name, func(t *testing.T) {
			t.Parallel()
			snap, err := policy.Load(c.edit(valid()).build(), pinned(), loadTime())
			if snap != nil {
				t.Errorf("Load returned a snapshot beside %v", err)
			}
			expectOnly(t, err, c.want)
			if c.within != nil {
				expectWithin(t, err, c.within)
			}
			var parse *rules.Error
			if errors.Is(c.want, policy.ErrDocument) && !errors.As(err, &parse) {
				t.Errorf("%v does not carry the parse's own refusal", err)
			}
		})
	}
}

// TestEveryFieldIsChanged holds changes() to the contract: a field the table
// never changes would be a field Load could ignore unseen.
func TestEveryFieldIsChanged(t *testing.T) {
	t.Parallel()
	changed := map[string]bool{}
	for _, c := range changes() {
		changed[c.field] = true
	}
	var names []string
	fields := (&controlv1.PolicyBundle{}).ProtoReflect().Descriptor().Fields()
	for i := range fields.Len() {
		names = append(names, string(fields.Get(i).Name()))
	}
	refFields := (&controlv1.PolicyBundleRef{}).ProtoReflect().Descriptor().Fields()
	for i := range refFields.Len() {
		names = append(names, "ref."+string(refFields.Get(i).Name()))
	}
	for _, name := range append(names, "unknown") {
		if !changed[name] {
			t.Errorf("no row changes %s", name)
		}
	}
	if len(names) != 10 {
		t.Errorf("the two messages have %d fields, want the 10 this table was written for", len(names))
	}
}

// defect is one change per check, for the order test.
type defect struct {
	want  error
	apply func(draft) draft
}

// defects are in the order Load runs its checks. The two that replace the
// document come before the digest's, which they would otherwise overwrite.
func defects() []defect {
	return []defect{
		{policy.ErrUnknownField, withUnknown(inBundle)},
		{policy.ErrSignatureAlg, setAlg("ed25519")},
		{policy.ErrKey, setKeyID("k3")},
		{policy.ErrSignature, setSignature(flipFirstBit)},
		{policy.ErrDocument, setDoc("[]")},
		{policy.ErrNotCanonical, setDoc(exampleRaw)},
		{policy.ErrDigest, setDigest(digestOf([]byte("other")))},
		{policy.ErrMismatch, setMaxStale(301)},
		{policy.ErrCreatedAt, setCreatedAt(timestamppb.New(loadTime()))},
	}
}

// TestLoadRunsTheChecksInOrder builds a bundle for every set of defects and
// expects the refusal of the earliest check among them, so any two checks run
// out of order give some set the wrong refusal. The two document defects
// replace one document, so no set holds both.
func TestLoadRunsTheChecksInOrder(t *testing.T) {
	t.Parallel()
	all := defects()
	const document, canonical = 4, 5
	for set := range 1 << len(all) {
		if set&(1<<document) != 0 && set&(1<<canonical) != 0 {
			continue
		}
		d, first := valid(), -1
		for i, defect := range all {
			if set&(1<<i) != 0 {
				d = defect.apply(d)
				if first < 0 {
					first = i
				}
			}
		}
		snap, err := policy.Load(d.build(), pinned(), loadTime())
		if first < 0 {
			if err != nil || snap == nil {
				t.Fatalf("no defect: (%v, %v), want a snapshot", snap, err)
			}
			continue
		}
		if snap != nil {
			t.Errorf("set %09b: a snapshot beside %v", set, err)
		}
		expectOnly(t, err, all[first].want)
	}
}

// padded is the example followed by spaces up to n bytes: a valid document
// the parser reads, and not its own canonical form.
func padded(n int) string {
	return exampleCanonical + strings.Repeat(" ", n-len(exampleCanonical))
}

// TestLoadRefusesAnOversizedCanonicalFirst: a canonical one byte over the
// parser's bound is refused ahead of every other check, and a canonical at the
// bound reaches the checks behind the signature.
func TestLoadRefusesAnOversizedCanonicalFirst(t *testing.T) {
	t.Parallel()
	const bound = 1 << 20
	over := valid().withDoc(padded(bound + 1))
	over = withUnknown(inBundle)(setAlg("ed25519")(setKeyID("k3")(setSignature(flipFirstBit)(over))))
	cases := []struct {
		name string
		d    draft
		want error
	}{
		{"one byte over, every other check failing too", over, policy.ErrTooLarge},
		{"one byte over, signed", valid().withDoc(padded(bound + 1)), policy.ErrTooLarge},
		{"at the bound, a bit of the signature flipped", setSignature(flipFirstBit)(valid().withDoc(padded(bound))), policy.ErrSignature},
		{"at the bound, signed", valid().withDoc(padded(bound)), policy.ErrNotCanonical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			snap, err := policy.Load(tc.d.build(), pinned(), loadTime())
			if snap != nil {
				t.Errorf("a snapshot beside %v", err)
			}
			expectOnly(t, err, tc.want)
		})
	}
}

func TestLoadRefusesNoBundle(t *testing.T) {
	t.Parallel()
	snap, err := policy.Load(nil, pinned(), loadTime())
	if snap != nil {
		t.Errorf("Load(nil) returned a snapshot")
	}
	expectOnly(t, err, policy.ErrNoBundle)
}

// TestLoadRefusesAKeyItCannotUse changes the keyring rather than the bundle:
// each is the key_id check's refusal, and wraps the bundle package's reason.
func TestLoadRefusesAKeyItCannotUse(t *testing.T) {
	t.Parallel()
	identity := make(ed25519.PublicKey, ed25519.PublicKeySize)
	identity[0] = 1
	cases := []struct {
		name   string
		keys   bundle.Keyring
		within error
	}{
		{"no keyring", nil, bundle.ErrUnknownKey},
		{"an empty keyring", bundle.Keyring{}, bundle.ErrUnknownKey},
		{"k1 pinned one byte short", bundle.Keyring{"k1": publicOf(1)[:31]}, bundle.ErrKeySize},
		{"k1 pinned as its private key", bundle.Keyring{"k1": ed25519.PublicKey(key(1))}, bundle.ErrKeySize},
		{"k1 pinned as the identity point", bundle.Keyring{"k1": identity}, bundle.ErrWeakKey},
	}
	b := valid().build()
	for _, tc := range cases {
		snap, err := policy.Load(b, tc.keys, loadTime())
		if snap != nil {
			t.Errorf("%s: a snapshot beside %v", tc.name, err)
		}
		expectOnly(t, err, policy.ErrKey)
		expectWithin(t, err, tc.within)
	}
	load(t, b)
}

// TestLoadRefusesAKnownFieldOfAnotherWireType sends canonical's field number a
// second time as a varint, as a receiver would decode it.
func TestLoadRefusesAKnownFieldOfAnotherWireType(t *testing.T) {
	t.Parallel()
	b := valid().build()
	wire := protowire.AppendVarint(protowire.AppendTag(marshal(t, b), 2, protowire.VarintType), 1)
	got := &controlv1.PolicyBundle{}
	if err := proto.Unmarshal(wire, got); err != nil {
		t.Fatal(err)
	}
	// The premise: the runtime kept the signed bytes and set the stray field
	// aside among the unknown ones.
	if !bytes.Equal(got.GetCanonical(), b.GetCanonical()) || len(got.ProtoReflect().GetUnknown()) == 0 {
		t.Fatalf("the runtime decoded the stray field differently: canonical kept %v, unknown %x",
			bytes.Equal(got.GetCanonical(), b.GetCanonical()), got.ProtoReflect().GetUnknown())
	}
	_, err := policy.Load(got, pinned(), loadTime())
	expectOnly(t, err, policy.ErrUnknownField)
}

func TestLoadLeavesItsInputsAlone(t *testing.T) {
	t.Parallel()
	b, keys := valid().build(), pinned()
	wire := marshal(t, b)
	want := map[string][]byte{}
	for id, k := range keys {
		want[id] = slices.Clone(k)
	}
	if _, err := policy.Load(b, keys, loadTime()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marshal(t, b), wire) {
		t.Error("Load changed the bundle it was handed")
	}
	if len(keys) != len(want) {
		t.Errorf("the keyring holds %d keys, want %d", len(keys), len(want))
	}
	for id, k := range want {
		if !bytes.Equal(keys[id], k) {
			t.Errorf("Load changed the key pinned as %s", id)
		}
	}
}

// TestTheSnapshotIgnoresLaterChangesToTheBundle makes every write a caller can
// make to the message it handed over, after Load returns. The snapshot still
// says what the signed document says.
func TestTheSnapshotIgnoresLaterChangesToTheBundle(t *testing.T) {
	t.Parallel()
	b := valid().build()
	snap := load(t, b)
	copy(b.Canonical[bytes.Index(b.Canonical, []byte("10000")):], "99999")
	b.Canonical = []byte(`{}`)
	b.Ref.BundleId, b.Ref.Version, b.Ref.Digest = "other", "other", "other"
	b.Ref.CreatedAt = timestamppb.New(loadTime())
	b.Signature[0] ^= 1
	b.SignatureAlg, b.KeyId, b.MaxStaleSeconds = "other", "other", 1

	want := &controlv1.PolicyBundleRef{BundleId: "payments", Version: "2026-09-10.1", Digest: exampleDigest}
	if !proto.Equal(snap.Ref(), want) {
		t.Errorf("Ref() = %v, want %v", snap.Ref(), want)
	}
	if snap.Serial() != 7 || snap.MaxStale() != 5*time.Minute || !snap.ConfirmedAt().Equal(loadTime()) {
		t.Errorf("serial %d, budget %v, confirmed %v; want 7, 5m0s, %v", snap.Serial(), snap.MaxStale(), snap.ConfirmedAt(), loadTime())
	}
	expectResult(t, snap.Evaluate(refund(), matchInputs()), approvalForRefund())
}
