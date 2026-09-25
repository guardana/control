package policy_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policy/match"
)

// What the tests check against. The package under test computes none of it:
// each value was computed by two implementations outside Go, which agree, and
// the canonical form equals the rules package's own golden.
const (
	// payloadType and signatureAlg are typed from ADR-0011.
	payloadType  = "application/vnd.agent-policy+json"
	signatureAlg = "ed25519-dsse"

	// exampleRaw is the example of the policy format as its author wrote it: a
	// valid document, and not its own canonical form.
	exampleRaw = `{
  "apiVersion": "agent-policy/v1alpha1",
  "bundle": {"id": "payments", "version": "2026-09-10.1", "serial": 7, "maxStaleSeconds": 300},
  "rules": [{
    "id": "refunds-in-prod-need-approval",
    "effect": "REQUIRE_APPROVAL",
    "obligations": [{"type": "cap_amount", "params": {"max": "10000"}}],
    "when": {
      "action":   {"name": ["refund"], "provider": ["payments"], "effect": ["TRANSACT"]},
      "resource": {"environment": ["prod"]}
    }
  }]
}
`
	exampleCanonical = `{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":"payments","maxStaleSeconds":300,"serial":7,"version":"2026-09-10.1"},"rules":[{"effect":"REQUIRE_APPROVAL","id":"refunds-in-prod-need-approval","obligations":[{"params":{"max":"10000"},"type":"cap_amount"}],"when":{"action":{"effect":["TRANSACT"],"name":["refund"],"provider":["payments"]},"resource":{"environment":["prod"]}}}]}`
	exampleDigest    = "sha256:040d29038eaa67e07d4e99953eaad6456f25bebe0ad05a15412ec259e4717e15"

	// exampleSignature is Ed25519, under the seed of 32 bytes 0x01, over the
	// DSSE encoding of exampleCanonical; seed01Public is that seed's public
	// key.
	exampleSignature = "6473beb1facd813acd8edb59c7142658f29e276d8bb489b3bf3c1f2e760b9ea145710bb4512a9d18113e46007dbe31cff6763f00b3f62e081f8ce27039fd4303"
	seed01Public     = "8a88e3dd7409f195fd52db2d3cba5d72ca6709bf1d94121bf3748801b40f6f5c"

	// everyFieldDigest and everyFieldSignature are the same two values for the
	// canonical form of the rules package's every-field sample.
	everyFieldDigest    = "sha256:53a9e2c4efba9927762693b7b2a7df97d3b1dc01add66e5037abfe2714a764e4"
	everyFieldSignature = "2979094f700a6653be374fe4f57e53c137e74cca2b2376dd6a4b1ef6e0520504ecd04d42f4ce2772ab9a313e639da5cc7657128584fbfe98b467f394c882090b"

	// unavailableCode is what a program nobody compiled answers with. It is
	// written out here, and a test holds it to the registry.
	unavailableCode = "POLICY_UNAVAILABLE"
)

// Where addUnknown puts a field that no message of the contract declares.
const (
	inBundle    = "bundle"
	inRef       = "ref"
	inCreatedAt = "ref.created_at"
)

// tb is what the helpers need of a test, so that a rapid.T can be one.
type tb interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// loadTime is the clock reading the tests load at. Its nanoseconds are not
// zero, so a time rounded or truncated anywhere shows.
func loadTime() time.Time {
	return time.Date(2026, time.September, 11, 12, 0, 0, 123456789, time.UTC)
}

// at is the clock reading n seconds after loadTime.
func at(n int) time.Time {
	return loadTime().Add(time.Duration(n) * time.Second)
}

// key is the private key whose seed is 32 bytes of b.
func key(b byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
}

// publicOf is the public half of key(b), which a private key carries in its
// last 32 bytes.
func publicOf(b byte) ed25519.PublicKey {
	return ed25519.PublicKey(slices.Clone(key(b)[ed25519.SeedSize:]))
}

// pinned is the keyring the tests verify against: k1 is seed 0x01 and k2 is
// seed 0x02.
func pinned() bundle.Keyring {
	return bundle.Keyring{"k1": publicOf(1), "k2": publicOf(2)}
}

// pae is DSSE's pre-authentication encoding under the policy payload type. It
// is written here from the specification, so that no test takes it from the
// package that signs.
func pae(body []byte) []byte { return paeOf(payloadType, body) }

func paeOf(payload string, body []byte) []byte {
	head := "DSSEv1 " + strconv.Itoa(len(payload)) + " " + payload + " " + strconv.Itoa(len(body)) + " "
	return append([]byte(head), body...)
}

// digestOf is ADR-0011's bundle digest, computed here from its formula.
func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func flipFirstBit(signature []byte) []byte {
	out := slices.Clone(signature)
	out[0] ^= 1
	return out
}

func mustHex(t tb, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return b
}

// draft is a bundle before it is signed and assembled. valid returns the
// example; a test changes what it is about, and build does the rest.
type draft struct {
	doc       []byte
	signer    ed25519.PrivateKey
	alg       string
	keyID     string
	bundleID  string
	version   string
	digest    string
	maxStale  int64
	createdAt *timestamppb.Timestamp
	// over replaces what the signature is computed over, when set.
	over      []byte
	editSig   func([]byte) []byte
	unknownIn []string
	// edit changes the assembled bundle last, for what the fields above cannot
	// say.
	edit func(*controlv1.PolicyBundle)
}

func valid() draft {
	return draft{
		doc:      []byte(exampleCanonical),
		signer:   key(1),
		alg:      signatureAlg,
		keyID:    "k1",
		bundleID: "payments",
		version:  "2026-09-10.1",
		digest:   exampleDigest,
		maxStale: 300,
	}
}

// withDoc signs other bytes and names them by their own digest, so that only
// a check that reads the document itself can refuse the result.
func (d draft) withDoc(doc string) draft {
	d.doc = []byte(doc)
	d.digest = digestOf(d.doc)
	return d
}

func (d draft) build() *controlv1.PolicyBundle {
	over := pae(d.doc)
	if d.over != nil {
		over = d.over
	}
	signature := ed25519.Sign(d.signer, over)
	if d.editSig != nil {
		signature = d.editSig(signature)
	}
	b := &controlv1.PolicyBundle{
		Ref: &controlv1.PolicyBundleRef{
			BundleId:  d.bundleID,
			Version:   d.version,
			Digest:    d.digest,
			CreatedAt: d.createdAt,
		},
		Canonical:       slices.Clone(d.doc),
		SignatureAlg:    d.alg,
		Signature:       signature,
		KeyId:           d.keyID,
		MaxStaleSeconds: d.maxStale,
	}
	for _, where := range d.unknownIn {
		addUnknown(b, where)
	}
	if d.edit != nil {
		d.edit(b)
	}
	return b
}

// addUnknown appends field 99, which no message of the contract declares.
func addUnknown(b *controlv1.PolicyBundle, where string) {
	field := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	switch where {
	case inBundle:
		b.ProtoReflect().SetUnknown(field)
	case inRef:
		b.Ref.ProtoReflect().SetUnknown(field)
	case inCreatedAt:
		if b.Ref.CreatedAt == nil {
			b.Ref.CreatedAt = &timestamppb.Timestamp{}
		}
		b.Ref.CreatedAt.ProtoReflect().SetUnknown(field)
	}
}

// document is a valid policy, in canonical form, whose identity and budget the
// caller chooses. Its one rule allows a READ.
func document(id, version string, serial, maxStale int64) string {
	return fmt.Sprintf(`{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":%q,"maxStaleSeconds":%d,"serial":%d,"version":%q},`+
		`"rules":[{"effect":"ALLOW","id":"reads","when":{"action":{"effect":["READ"]}}}]}`, id, maxStale, serial, version)
}

// signedDocument is document(id, version, serial, 300), signed by k1 and named
// by its own identity and digest.
func signedDocument(id, version string, serial int64) *controlv1.PolicyBundle {
	return signedWithBudget(id, version, serial, 300)
}

func signedWithBudget(id, version string, serial, maxStale int64) *controlv1.PolicyBundle {
	d := valid().withDoc(document(id, version, serial, maxStale))
	d.bundleID, d.version, d.maxStale = id, version, maxStale
	return d.build()
}

func load(t tb, b *controlv1.PolicyBundle) *policy.Snapshot {
	t.Helper()
	snap, err := policy.Load(b, pinned(), loadTime())
	if err != nil {
		t.Fatalf("Load refused a bundle this test builds as valid: %v", err)
	}
	if snap == nil {
		t.Fatalf("Load returned neither a snapshot nor a refusal")
	}
	return snap
}

func install(t tb, h *policy.Holder, b *controlv1.PolicyBundle, when time.Time) {
	t.Helper()
	if err := h.Install(b, pinned(), when); err != nil {
		t.Fatalf("Install refused a bundle this test expects it to take: %v", err)
	}
}

// expectCurrent fails unless the holder's snapshot has this serial and digest
// and was last confirmed at confirmed.
func expectCurrent(t tb, h *policy.Holder, serial int64, digest string, confirmed time.Time) {
	t.Helper()
	cur := h.Current()
	if cur == nil {
		t.Fatalf("Current() = nil, want serial %d", serial)
	}
	if cur.Serial() != serial || cur.Ref().GetDigest() != digest || !cur.ConfirmedAt().Equal(confirmed) {
		t.Fatalf("Current() has serial %d, digest %s, confirmed %v; want %d, %s, %v",
			cur.Serial(), cur.Ref().GetDigest(), cur.ConfirmedAt(), serial, digest, confirmed)
	}
}

// loadChecks are Load's refusals, in the order Load runs its checks.
func loadChecks() []error {
	return []error{
		policy.ErrNoBundle,
		policy.ErrTooLarge,
		policy.ErrUnknownField,
		policy.ErrSignatureAlg,
		policy.ErrKey,
		policy.ErrSignature,
		policy.ErrDocument,
		policy.ErrNotCanonical,
		policy.ErrDigest,
		policy.ErrMismatch,
		policy.ErrCreatedAt,
	}
}

// sentinels are every refusal the package names: Load's, then Sign's and
// Install's own.
func sentinels() []error {
	return append(loadChecks(), policy.ErrSigningKey, policy.ErrRollback, policy.ErrSerialReused, policy.ErrBundlePin)
}

// expectOnly fails unless err is the refusal want, and none of the others.
func expectOnly(t tb, err, want error) {
	t.Helper()
	if err == nil {
		t.Fatalf("accepted, want the refusal %q", want)
	}
	for _, s := range sentinels() {
		if got, wanted := errors.Is(err, s), errors.Is(s, want); got != wanted {
			t.Errorf("errors.Is(%q, %q) = %v, want %v", err, s, got, wanted)
		}
	}
}

// expectWithin fails unless err wraps the bundle package's refusal want, and
// none of its others.
func expectWithin(t tb, err, want error) {
	t.Helper()
	for _, s := range []error{bundle.ErrKeySize, bundle.ErrKeyPair, bundle.ErrUnknownKey, bundle.ErrWeakKey, bundle.ErrSignature} {
		if got, wanted := errors.Is(err, s), errors.Is(s, want); got != wanted {
			t.Errorf("errors.Is(%q, %q) = %v, want %v", err, s, got, wanted)
		}
	}
}

// refund is a call the example's one rule matches: a TRANSACT refund in prod.
func refund() *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		Action:   &controlv1.Action{Name: "refund", Provider: "payments", Effect: controlv1.EffectClass_EFFECT_CLASS_TRANSACT},
		Resource: &controlv1.Resource{Environment: "prod"},
	}
}

// readCall is a READ. The example's rule is false for it, and the rule of
// document() allows it.
func readCall() *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{Action: &controlv1.Action{Effect: controlv1.EffectClass_EFFECT_CLASS_READ}}
}

// approvalForRefund is what the example decides for refund(), typed from the
// document: its one rule matched, with its obligation and its default reason.
func approvalForRefund() match.Result {
	return match.Result{
		Verdict:     controlv1.Verdict_VERDICT_REQUIRE_APPROVAL,
		Determinate: controlv1.Verdict_VERDICT_REQUIRE_APPROVAL,
		RuleIDs:     []string{"refunds-in-prod-need-approval"},
		ReasonCodes: []string{"APPROVAL_REQUIRED"},
		Obligations: []*controlv1.Obligation{{Type: "cap_amount", Params: map[string]string{"max": "10000"}}},
	}
}

// expectResult fails unless got is want, every list compared in order.
func expectResult(t tb, got, want match.Result) {
	t.Helper()
	if got.Verdict != want.Verdict || got.Determinate != want.Determinate {
		t.Errorf("verdict %v, determinate %v; want %v, %v", got.Verdict, got.Determinate, want.Verdict, want.Determinate)
	}
	if !slices.Equal(got.RuleIDs, want.RuleIDs) || !slices.Equal(got.Indeterminate, want.Indeterminate) {
		t.Errorf("rules %q, indeterminate %q; want %q, %q", got.RuleIDs, got.Indeterminate, want.RuleIDs, want.Indeterminate)
	}
	if !slices.Equal(got.ReasonCodes, want.ReasonCodes) {
		t.Errorf("reason codes %q, want %q", got.ReasonCodes, want.ReasonCodes)
	}
	equal := func(a, b *controlv1.Obligation) bool { return proto.Equal(a, b) }
	if !slices.EqualFunc(got.Obligations, want.Obligations, equal) {
		t.Errorf("obligations %v, want %v", got.Obligations, want.Obligations)
	}
}

func sampleDocument(t tb, name string) []byte {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(filepath.Join("..", "..", "testdata", "policy", "documents")), name)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return raw
}

func marshal(t tb, m proto.Message) []byte {
	t.Helper()
	wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return wire
}
