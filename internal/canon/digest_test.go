// The action digest and its golden fixtures. An external test package on
// purpose: it uses only the exported surface, which is the surface a Python or
// TypeScript implementation has to reproduce, and it can import pkg/contract to
// prove every fixture is an envelope the frozen contract accepts.
package canon_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/pkg/contract"
)

// The fixtures are at the repository root rather than beside this package: they
// are the contract a later SDK in another language is checked against, and that
// SDK does not read Go packages.
const fixtureDir = "../../testdata/digest"

var fixtureNames = []string{"minimal", "refund_prod", "delegated", "mutated_amount", "escapes"}

// The tags are written out here rather than read from the package. A test that
// reads a constant from the implementation cannot notice the implementation
// changing it, and changing either of these invalidates every stored approval
// (ADR-0010).
const (
	actionTag    = "agent-action-digest/v1\n"
	bindingTag   = "agent-approval-binding/v1\n"
	argumentsTag = "agent-arguments-hash/v1\n"
)

func TestDomainTagsAreFixed(t *testing.T) {
	if canon.DigestDomainV1 != actionTag {
		t.Errorf("action domain tag = %q, want %q", canon.DigestDomainV1, actionTag)
	}
	if canon.BindingDomainV1 != bindingTag {
		t.Errorf("binding domain tag = %q, want %q", canon.BindingDomainV1, bindingTag)
	}
	if canon.ArgumentsDomainV1 != argumentsTag {
		t.Errorf("arguments domain tag = %q, want %q", canon.ArgumentsDomainV1, argumentsTag)
	}
	for name, tag := range map[string]string{"action": actionTag, "binding": bindingTag, "arguments": argumentsTag} {
		if !strings.HasSuffix(tag, "\n") {
			t.Errorf("%s tag does not end with a newline: %q", name, tag)
		}
		if strings.Contains(strings.ToLower(tag), "guard") {
			t.Errorf("%s tag carries a product name: %q", name, tag)
		}
	}
}

// fixture is the on-disk shape. The two documents stay raw: the envelope goes
// to protojson, and the authorized arguments are hashed as the bytes the file
// holds, which is what an implementation in another language reads too.
type fixture struct {
	Envelope       json.RawMessage `json:"envelope"`
	AuthorizedArgs json.RawMessage `json:"authorized_args"`
	ExpectedDigest string          `json:"expected_digest"`
}

func loadFixture(t testing.TB, name string) fixture {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(fixtureDir), name+".json")
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f fixture
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	if len(f.AuthorizedArgs) == 0 {
		t.Fatalf("fixture %s has no authorized_args", name)
	}
	return f
}

// envelopeOf decodes and validates: a golden computed over an envelope the
// frozen contract would refuse is a golden for an action that cannot happen.
func envelopeOf(t testing.TB, f fixture, name string) *controlv1.ActionEnvelope {
	t.Helper()
	env, err := contract.DecodeJSON(f.Envelope)
	if err != nil {
		t.Fatalf("fixture %s does not validate: %v", name, err)
	}
	return env
}

func digestOf(t testing.TB, env *controlv1.ActionEnvelope, args []byte) string {
	t.Helper()
	got, err := canon.DigestV1(env, args)
	if err != nil {
		t.Fatalf("DigestV1: %v", err)
	}
	return got
}

// TestGoldenFixtures is the contract itself. Each value was computed once, by
// this test reporting it for a fixture that had no expected_digest, and pasted
// in; from then on a change to any rule in ADR-0005 fails here.
func TestGoldenFixtures(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			f := loadFixture(t, name)
			env := envelopeOf(t, f, name)

			body, err := canon.Canonicalize(mustAction(t, env, f.AuthorizedArgs))
			if err != nil {
				t.Fatalf("Canonicalize: %v", err)
			}
			t.Logf("canonical bytes (%d): %s", len(body), body)

			got := digestOf(t, env, f.AuthorizedArgs)
			if f.ExpectedDigest == "" {
				t.Fatalf("fixture has no expected_digest; computed %s", got)
			}
			if got != f.ExpectedDigest {
				t.Errorf("digest = %s, want %s", got, f.ExpectedDigest)
			}
			// The digest is sha256 over the tag and those exact bytes, and
			// nothing else: recomputed here from the primitive so the test
			// pins the construction and not only DigestV1's agreement with
			// itself.
			sum := sha256.Sum256(append([]byte(actionTag), body...))
			if want := "sha256:" + hex.EncodeToString(sum[:]); got != want {
				t.Errorf("digest = %s, but sha256(tag||body) = %s", got, want)
			}
			assertDigestForm(t, got)
		})
	}
}

func mustAction(t testing.TB, env *controlv1.ActionEnvelope, args []byte) map[string]any {
	t.Helper()
	action, err := canon.CanonicalAction(env, args)
	if err != nil {
		t.Fatalf("CanonicalAction: %v", err)
	}
	return action
}

// assertDigestForm holds the case rule: these strings are compared for equality
// to authorize a call, so an upper-case digest is a different string.
func assertDigestForm(t testing.TB, digest string) {
	t.Helper()
	hexPart, ok := strings.CutPrefix(digest, "sha256:")
	switch {
	case !ok:
		t.Errorf("digest %q has no sha256: prefix", digest)
	case len(hexPart) != 64:
		t.Errorf("digest %q has %d hex digits, want 64", digest, len(hexPart))
	case strings.TrimLeft(hexPart, "0123456789abcdef") != "":
		t.Errorf("digest %q is not lowercase hex", digest)
	}
}

func TestGoldenDigestsAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, name := range fixtureNames {
		f := loadFixture(t, name)
		if other, ok := seen[f.ExpectedDigest]; ok {
			t.Errorf("%s and %s share the digest %s", other, name, f.ExpectedDigest)
		}
		seen[f.ExpectedDigest] = name
	}
	if len(seen) != len(fixtureNames) {
		t.Errorf("%d distinct digests over %d fixtures", len(seen), len(fixtureNames))
	}
}

// TestMutatedAmountDiffersOnlyInTheArgument is what makes the mutated_amount
// golden mean something: the two envelopes are the same message, so the two
// digests can only differ because of the argument.
func TestMutatedAmountDiffersOnlyInTheArgument(t *testing.T) {
	base, mutated := loadFixture(t, "refund_prod"), loadFixture(t, "mutated_amount")
	baseEnv := envelopeOf(t, base, "refund_prod")
	mutatedEnv := envelopeOf(t, mutated, "mutated_amount")
	if !proto.Equal(baseEnv, mutatedEnv) {
		t.Fatal("the two fixtures do not carry the same envelope")
	}

	// Both key sets, and every value as canonical bytes: a walk over one side's
	// keys never visits a member only the other side has, and a value read into
	// a Go map cannot tell a missing member from null.
	baseArgs, mutatedArgs := membersOf(t, base.AuthorizedArgs), membersOf(t, mutated.AuthorizedArgs)
	want, got := slices.Sorted(maps.Keys(baseArgs)), slices.Sorted(maps.Keys(mutatedArgs))
	if !slices.Equal(got, want) {
		t.Fatalf("the arguments have the members %q and %q", want, got)
	}
	var differing []string
	for _, key := range want {
		if !bytes.Equal(canonicalOf(t, baseArgs[key]), canonicalOf(t, mutatedArgs[key])) {
			differing = append(differing, key)
		}
	}
	if !slices.Equal(differing, []string{"amount_minor"}) {
		t.Errorf("arguments differ in %v, want only amount_minor", differing)
	}
	if base.ExpectedDigest == mutated.ExpectedDigest {
		t.Error("one changed argument produced the same digest")
	}
}

// membersOf splits an arguments document into its members, each value kept as
// the bytes the document holds.
func membersOf(t testing.TB, doc []byte) map[string]json.RawMessage {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(doc, &members); err != nil {
		t.Fatalf("split the arguments into members: %v", err)
	}
	return members
}

func canonicalOf(t testing.TB, raw json.RawMessage) []byte {
	t.Helper()
	out, err := canon.CanonicalizeJSON(raw)
	if err != nil {
		t.Fatalf("CanonicalizeJSON(%s): %v", raw, err)
	}
	return out
}

// TestEmptyEntriesAreKept: an empty value is a value, an empty key is a key and
// an empty scope is a scope. Dropping one gives two envelopes that validate one
// digest, and a policy testing for presence tells the two apart.
func TestEmptyEntriesAreKept(t *testing.T) {
	f := loadFixture(t, "refund_prod")
	base := envelopeOf(t, f, "refund_prod")
	cases := []struct {
		name          string
		with, without func(*controlv1.ActionEnvelope)
	}{
		{
			"an attribute with an empty value",
			func(e *controlv1.ActionEnvelope) { e.Principal.Attributes = map[string]string{"break_glass": ""} },
			func(e *controlv1.ActionEnvelope) { e.Principal.Attributes = map[string]string{} },
		},
		{
			"an empty scope",
			func(e *controlv1.ActionEnvelope) { e.Delegation[0].Scopes = []string{""} },
			func(e *controlv1.ActionEnvelope) { e.Delegation[0].Scopes = nil },
		},
		{
			"a label with an empty key",
			func(e *controlv1.ActionEnvelope) { e.Resource.Labels = map[string]string{"": "x"} },
			func(e *controlv1.ActionEnvelope) { e.Resource.Labels = map[string]string{} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			with := proto.Clone(base).(*controlv1.ActionEnvelope)
			without := proto.Clone(base).(*controlv1.ActionEnvelope)
			tc.with(with)
			tc.without(without)
			if proto.Equal(with, without) {
				t.Fatal("the two envelopes are one message, so nothing is under test")
			}
			if digestOf(t, with, f.AuthorizedArgs) == digestOf(t, without, f.AuthorizedArgs) {
				t.Errorf("%s was dropped: the two envelopes share a digest", tc.name)
			}
		})
	}
}

// TestKeyAndScopeOrderDoNotChangeTheDigest is the fifth case the brief asks
// for, built mechanically rather than as a second fixture: re-serializing the
// envelope through a Go map puts its members in another order, and the scopes
// of every hop are reversed on the way.
func TestKeyAndScopeOrderDoNotChangeTheDigest(t *testing.T) {
	for _, name := range fixtureNames {
		t.Run(name, func(t *testing.T) {
			f := loadFixture(t, name)
			want := digestOf(t, envelopeOf(t, f, name), f.AuthorizedArgs)

			var doc map[string]any
			if err := json.Unmarshal(f.Envelope, &doc); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			reverseScopes(doc)
			// json.Marshal writes object members in sorted key order, which is
			// not the order the fixture is written in.
			reordered, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}
			env := &controlv1.ActionEnvelope{}
			if err := protojson.Unmarshal(reordered, env); err != nil {
				t.Fatalf("protojson: %v", err)
			}
			if got := digestOf(t, env, reformat(t, f.AuthorizedArgs)); got != want {
				t.Errorf("reordered digest = %s, want %s", got, want)
			}
		})
	}
}

func reverseScopes(doc map[string]any) {
	hops, _ := doc["delegation"].([]any)
	for _, hop := range hops {
		m, ok := hop.(map[string]any)
		if !ok {
			continue
		}
		if scopes, ok := m["scopes"].([]any); ok {
			slices.Reverse(scopes)
		}
	}
}

// reformat rewrites the arguments document with different whitespace and member
// order, so the digest is shown to depend on the value and not on the bytes.
func reformat(t testing.TB, raw []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal arguments: %v", err)
	}
	out, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}
	return out
}

// TestEveryFieldIsMaterialized pins the canonical form of an empty envelope.
// It is the rule that stops an implementation building the map from protojson
// output, which omits an empty scalar, from disagreeing with one reading the
// struct: every key is present, at its proto zero value, and no value is null.
func TestEveryFieldIsMaterialized(t *testing.T) {
	const want = `{"action":{"effect":"EFFECT_CLASS_UNSPECIFIED","kind":"","name":"",` +
		`"protocol":"","provider":""},"agent":{"framework":"","id":"","instanceId":"",` +
		`"modelRef":"","version":""},"authorizedArguments":{},"delegation":[],` +
		`"destination":{"host":"","trustZone":"TRUST_ZONE_UNSPECIFIED"},"environment":"",` +
		`"principal":{"attributes":{},"authnStrength":"","id":"","tenantId":"","type":""},` +
		`"projectId":"",` +
		`"resource":{"environment":"","id":"","labels":{},"tenantId":"","type":""},` +
		`"tenantId":""}`

	body, err := canon.Canonicalize(mustAction(t, &controlv1.ActionEnvelope{}, nil))
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	if string(body) != want {
		t.Errorf("empty envelope canonicalizes to\n%s\nwant\n%s", body, want)
	}
	if bytes.Contains(body, []byte("null")) {
		t.Error("null appears in the canonical form of an empty envelope")
	}
}

// includedMessages is the ADR-0005 field set as this test's own copy: the
// canonical key of each included message, and the proto fields inside it that
// the digest deliberately leaves out. Adding a field to one of these messages
// fails TestFieldSetMatchesTheContract, which is the point: a new field either
// enters the digest and invalidates every stored approval, or it is listed
// here, and both are decisions rather than accidents.
var includedMessages = map[string][]string{
	"principal":   nil,
	"agent":       nil,
	"delegation":  {"issued_at", "expires_at"},
	"action":      nil,
	"resource":    nil,
	"destination": nil,
}

// includedEnvelopeStrings are the envelope's own fields that enter the digest as
// top-level members of the canonical action (ADR-0011). They say where the
// action happens, and two attempts at one action never differ in them: without
// them an approval granted in one project, tenant or environment would match the
// identical action in another.
var includedEnvelopeStrings = []string{"project_id", "tenant_id", "environment"}

// excludedEnvelopeFields is the rest of the same table: an envelope field that
// is not in the digest at all. Identifiers and timestamps differ between two
// attempts at one action. The data labels and the run context can stay out
// because an approval is honoured only in the trail of the request it was
// requested for, where they cannot change (ADR-0011). The arguments message
// holds a hash and a preview of what the digest already carries whole.
var excludedEnvelopeFields = []string{
	"schema_version", "request_id", "trace_id", "span_id", "occurred_at",
	"data", "arguments", "context",
}

func envelopeDescriptor() protoreflect.MessageDescriptor {
	return (&controlv1.ActionEnvelope{}).ProtoReflect().Descriptor()
}

// TestFieldSetMatchesTheContract compares the canonical action against the
// descriptors rather than against a list written by the same hand that wrote
// the map, so a field added to the frozen contract cannot slip out of the
// digest unnoticed.
func TestFieldSetMatchesTheContract(t *testing.T) {
	fields := envelopeDescriptor().Fields()
	var seen []string
	for i := range fields.Len() {
		seen = append(seen, string(fields.Get(i).Name()))
	}
	want := slices.Concat(slices.Sorted(maps.Keys(includedMessages)), includedEnvelopeStrings, excludedEnvelopeFields)
	slices.Sort(seen)
	slices.Sort(want)
	if !slices.Equal(seen, want) {
		t.Errorf("the envelope's fields are\n%v\nbut the digest tables cover\n%v", seen, want)
	}

	f := loadFixture(t, "delegated")
	action := mustAction(t, envelopeOf(t, f, "delegated"), f.AuthorizedArgs)

	// The names come from the descriptors, not from a list written beside the
	// map the implementation builds.
	top := slices.Sorted(maps.Keys(action))
	wantTop := append(slices.Sorted(maps.Keys(includedMessages)), "authorizedArguments")
	for _, name := range includedEnvelopeStrings {
		wantTop = append(wantTop, fields.ByName(protoreflect.Name(name)).JSONName())
	}
	slices.Sort(wantTop)
	if !slices.Equal(top, wantTop) {
		t.Errorf("canonical action keys = %v, want %v", top, wantTop)
	}

	for key, skip := range includedMessages {
		t.Run(key, func(t *testing.T) {
			fd := fields.ByName(protoreflect.Name(key))
			var want []string
			for i := range fd.Message().Fields().Len() {
				field := fd.Message().Fields().Get(i)
				if !slices.Contains(skip, string(field.Name())) {
					want = append(want, field.JSONName())
				}
			}
			got := slices.Sorted(maps.Keys(memberOf(t, action, key)))
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("keys = %v, want %v", got, want)
			}
		})
	}
}

// memberOf returns the object the canonical action holds at key, reaching into
// the first hop for the repeated one.
func memberOf(t testing.TB, action map[string]any, key string) map[string]any {
	t.Helper()
	if key == "delegation" {
		hops, ok := action[key].([]any)
		if !ok || len(hops) == 0 {
			t.Fatalf("delegation is %T with no hop to read", action[key])
		}
		return hops[0].(map[string]any)
	}
	m, ok := action[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want an object", key, action[key])
	}
	return m
}

// mutation changes exactly one field. It may replace the arguments document,
// which is the one part of the field set that is not an envelope field.
type mutation struct {
	pointer string
	apply   func(env *controlv1.ActionEnvelope, args []byte) []byte
}

func env0(f func(*controlv1.ActionEnvelope)) func(*controlv1.ActionEnvelope, []byte) []byte {
	return func(env *controlv1.ActionEnvelope, args []byte) []byte {
		f(env)
		return args
	}
}

// TestScopeMembersAreTheEnvelopesOwn pins where the three top-level members
// come from. The principal and the resource carry a tenant and an environment
// of their own, and every fixture happens to give all three the same value, so
// a member read from the wrong message would pass the goldens.
func TestScopeMembersAreTheEnvelopesOwn(t *testing.T) {
	env := &controlv1.ActionEnvelope{
		ProjectId:   "project-of-the-envelope",
		TenantId:    "tenant-of-the-envelope",
		Environment: "environment-of-the-envelope",
		Principal:   &controlv1.Principal{TenantId: "tenant-of-the-principal"},
		Resource: &controlv1.Resource{
			TenantId:    "tenant-of-the-resource",
			Environment: "environment-of-the-resource",
		},
	}
	action := mustAction(t, env, nil)
	for key, want := range map[string]string{
		"projectId":   "project-of-the-envelope",
		"tenantId":    "tenant-of-the-envelope",
		"environment": "environment-of-the-envelope",
	} {
		if got := action[key]; got != want {
			t.Errorf("%s = %#v, want %q", key, got, want)
		}
	}
	if got := memberOf(t, action, "resource")["environment"]; got != "environment-of-the-resource" {
		t.Errorf("resource.environment = %#v, want the resource's own", got)
	}
}

var includedMutations = []mutation{
	// tenant-other and staging are also the values the principal's and the
	// resource's mutations below use: a member read from the wrong field then
	// either leaves the digest where it was or collides with another mutation.
	{"/projectId", env0(func(e *controlv1.ActionEnvelope) { e.ProjectId = "proj-other" })},
	{"/tenantId", env0(func(e *controlv1.ActionEnvelope) { e.TenantId = "tenant-other" })},
	{"/environment", env0(func(e *controlv1.ActionEnvelope) { e.Environment = "staging" })},
	{"/principal/id", env0(func(e *controlv1.ActionEnvelope) { e.Principal.Id = "user-other" })},
	{"/principal/type", env0(func(e *controlv1.ActionEnvelope) { e.Principal.Type = "service" })},
	{"/principal/authnStrength", env0(func(e *controlv1.ActionEnvelope) { e.Principal.AuthnStrength = "aal3" })},
	{"/principal/tenantId", env0(func(e *controlv1.ActionEnvelope) { e.Principal.TenantId = "tenant-other" })},
	{"/principal/attributes", env0(func(e *controlv1.ActionEnvelope) { e.Principal.Attributes["shift"] = "night" })},
	{"/agent/id", env0(func(e *controlv1.ActionEnvelope) { e.Agent.Id = "agent-other" })},
	{"/agent/instanceId", env0(func(e *controlv1.ActionEnvelope) { e.Agent.InstanceId = "inst-other" })},
	{"/agent/framework", env0(func(e *controlv1.ActionEnvelope) { e.Agent.Framework = "other-harness" })},
	{"/agent/version", env0(func(e *controlv1.ActionEnvelope) { e.Agent.Version = "0.2.0" })},
	{"/agent/modelRef", env0(func(e *controlv1.ActionEnvelope) { e.Agent.ModelRef = "fixture-model-z" })},
	{"/delegation/0/from", env0(func(e *controlv1.ActionEnvelope) { e.Delegation[0].From = "user-other" })},
	{"/delegation/0/to", env0(func(e *controlv1.ActionEnvelope) { e.Delegation[0].To = "agent-other" })},
	{"/delegation/0/scopes", env0(func(e *controlv1.ActionEnvelope) {
		e.Delegation[0].Scopes = append(e.Delegation[0].Scopes, "refunds:approve")
	})},
	{"/delegation/0/reason", env0(func(e *controlv1.ActionEnvelope) { e.Delegation[0].Reason = "other reason" })},
	{"/action/kind", env0(func(e *controlv1.ActionEnvelope) { e.Action.Kind = "http_call" })},
	{"/action/name", env0(func(e *controlv1.ActionEnvelope) { e.Action.Name = "issue_credit" })},
	{"/action/protocol", env0(func(e *controlv1.ActionEnvelope) { e.Action.Protocol = "a2a" })},
	{"/action/effect", env0(func(e *controlv1.ActionEnvelope) {
		e.Action.Effect = controlv1.EffectClass_EFFECT_CLASS_WRITE
	})},
	{"/action/provider", env0(func(e *controlv1.ActionEnvelope) { e.Action.Provider = "fixture-other-server" })},
	{"/resource/type", env0(func(e *controlv1.ActionEnvelope) { e.Resource.Type = "invoice" })},
	{"/resource/id", env0(func(e *controlv1.ActionEnvelope) { e.Resource.Id = "pay-fixture-0002" })},
	{"/resource/tenantId", env0(func(e *controlv1.ActionEnvelope) { e.Resource.TenantId = "tenant-other" })},
	{"/resource/environment", env0(func(e *controlv1.ActionEnvelope) { e.Resource.Environment = "staging" })},
	{"/resource/labels", env0(func(e *controlv1.ActionEnvelope) { e.Resource.Labels["tier"] = "silver" })},
	{"/destination/trustZone", env0(func(e *controlv1.ActionEnvelope) {
		e.Destination.TrustZone = controlv1.TrustZone_TRUST_ZONE_UNTRUSTED_EXTERNAL
	})},
	{"/destination/host", env0(func(e *controlv1.ActionEnvelope) { e.Destination.Host = "other.example.test" })},
	{"/authorizedArguments", func(_ *controlv1.ActionEnvelope, _ []byte) []byte {
		return []byte(`{"amount_minor":1251}`)
	}},
}

// TestIncludedFieldsChangeTheDigest is invariant 6 as a test: an approval binds
// to one exact action, so changing any field of the set has to move the digest.
func TestIncludedFieldsChangeTheDigest(t *testing.T) {
	f := loadFixture(t, "refund_prod")
	base := envelopeOf(t, f, "refund_prod")
	want := digestOf(t, base, f.AuthorizedArgs)

	covered := make([]string, 0, len(includedMutations))
	seen := map[string]string{want: "unmutated"}
	for _, m := range includedMutations {
		t.Run(m.pointer, func(t *testing.T) {
			env := proto.Clone(base).(*controlv1.ActionEnvelope)
			args := m.apply(env, slices.Clone([]byte(f.AuthorizedArgs)))
			got := digestOf(t, env, args)
			if got == want {
				t.Errorf("changing %s did not change the digest", m.pointer)
			}
			if other, ok := seen[got]; ok {
				t.Errorf("changing %s collides with %s", m.pointer, other)
			}
			seen[got] = m.pointer
		})
		covered = append(covered, m.pointer)
	}
	assertCoversFieldSet(t, covered)
}

// assertCoversFieldSet derives the pointers of the field set from the
// descriptors, so a field added to the contract leaves this table incomplete
// and fails rather than going untested.
func assertCoversFieldSet(t *testing.T, covered []string) {
	t.Helper()
	fields := envelopeDescriptor().Fields()
	want := []string{"/authorizedArguments"}
	for _, name := range includedEnvelopeStrings {
		want = append(want, "/"+fields.ByName(protoreflect.Name(name)).JSONName())
	}
	for key, skip := range includedMessages {
		prefix := "/" + key
		if key == "delegation" {
			prefix += "/0"
		}
		message := fields.ByName(protoreflect.Name(key)).Message()
		for i := range message.Fields().Len() {
			fd := message.Fields().Get(i)
			if !slices.Contains(skip, string(fd.Name())) {
				want = append(want, prefix+"/"+fd.JSONName())
			}
		}
	}
	slices.Sort(want)
	slices.Sort(covered)
	if !slices.Equal(covered, want) {
		t.Errorf("mutations cover\n%v\nbut the field set is\n%v", covered, want)
	}
}

// excludedMutations names its fields by proto path: none of them appears in the
// canonical action at all, so none of them has a JSON pointer.
var excludedMutations = []mutation{
	{"schema_version", env0(func(e *controlv1.ActionEnvelope) { e.SchemaVersion = "1.7" })},
	{"request_id", env0(func(e *controlv1.ActionEnvelope) { e.RequestId = "req-retry-1" })},
	{"trace_id", env0(func(e *controlv1.ActionEnvelope) { e.TraceId = "00000000000000000000000000000001" })},
	{"span_id", env0(func(e *controlv1.ActionEnvelope) { e.SpanId = "0000000000000001" })},
	{"occurred_at", env0(func(e *controlv1.ActionEnvelope) { e.OccurredAt = timestamppb.New(clock()) })},
	{"data", env0(func(e *controlv1.ActionEnvelope) {
		e.Data.ContainsSecrets = true
		e.Data.Sources = append(e.Data.Sources, "another-store")
	})},
	{"arguments", env0(func(e *controlv1.ActionEnvelope) {
		e.Arguments.CanonicalHash = "sha256:" + strings.Repeat("f", 64)
		e.Arguments.RedactedPreview = "{\"amount_minor\":\"[hidden]\"}"
		e.Arguments.SchemaRef = "fixture.refund.v2"
		e.Arguments.RedactionProfile = "fixture-loose-v1"
	})},
	{"context", env0(func(e *controlv1.ActionEnvelope) {
		e.Context.StepId = "step-9"
		e.Context.Risk = "low"
		e.Context.Budgets["tokens"] = 99
		e.Context.Tags = append(e.Context.Tags, "second-attempt")
	})},
	{"delegation[0].issued_at", env0(func(e *controlv1.ActionEnvelope) {
		e.Delegation[0].IssuedAt = timestamppb.New(clock())
	})},
	{"delegation[0].expires_at", env0(func(e *controlv1.ActionEnvelope) {
		e.Delegation[0].ExpiresAt = timestamppb.New(clock())
	})},
}

// TestExcludedFieldsDoNotChangeTheDigest is the property that lets an approval
// match a retry of the same action at all.
func TestExcludedFieldsDoNotChangeTheDigest(t *testing.T) {
	f := loadFixture(t, "refund_prod")
	base := envelopeOf(t, f, "refund_prod")
	want := digestOf(t, base, f.AuthorizedArgs)

	covered := make([]string, 0, len(excludedMutations))
	for _, m := range excludedMutations {
		t.Run(m.pointer, func(t *testing.T) {
			env := proto.Clone(base).(*controlv1.ActionEnvelope)
			args := m.apply(env, slices.Clone([]byte(f.AuthorizedArgs)))
			if proto.Equal(env, base) {
				t.Fatalf("the mutation for %s changed nothing", m.pointer)
			}
			if got := digestOf(t, env, args); got != want {
				t.Errorf("changing %s changed the digest: %s, want %s", m.pointer, got, want)
			}
		})
		covered = append(covered, m.pointer)
	}
	want2 := slices.Concat(excludedEnvelopeFields,
		[]string{"delegation[0].issued_at", "delegation[0].expires_at"})
	slices.Sort(want2)
	slices.Sort(covered)
	if !slices.Equal(covered, want2) {
		t.Errorf("mutations cover\n%v\nbut the exclusion list is\n%v", covered, want2)
	}
}

// clock returns one fixed instant. No digest may depend on it, which is what
// the excluded table asserts; a real clock here would make a failure depend on
// the minute the suite ran.
func clock() time.Time { return time.Date(2027, 5, 4, 3, 2, 1, 0, time.UTC) }

// TestDelegationOrderIsTheAuthorityChain pins the one ordering the digest keeps.
// Reversing the hops inverts the "child exceeds parent" check, so two chains
// that differ only in direction must not share a digest.
func TestDelegationOrderIsTheAuthorityChain(t *testing.T) {
	f := loadFixture(t, "delegated")
	base := envelopeOf(t, f, "delegated")
	if len(base.GetDelegation()) < 2 {
		t.Fatalf("the delegated fixture has %d hops, want at least 2", len(base.GetDelegation()))
	}
	want := digestOf(t, base, f.AuthorizedArgs)

	reversed := proto.Clone(base).(*controlv1.ActionEnvelope)
	slices.Reverse(reversed.Delegation)
	if got := digestOf(t, reversed, f.AuthorizedArgs); got == want {
		t.Error("reversing the authority chain did not change the digest")
	}

	action := mustAction(t, base, f.AuthorizedArgs)
	hops := action["delegation"].([]any)
	for i, hop := range hops {
		if got, want := hop.(map[string]any)["from"], base.GetDelegation()[i].GetFrom(); got != want {
			t.Errorf("hop %d is from %q, want %q", i, got, want)
		}
	}
}

// TestScopesAreSortedNotOrdered pins the other half: a scope list is a set, so
// its order is normalized. The expected value is spelled out because the trap
// is invisible otherwise: U+1F600 encodes to the surrogate pair D83D DE00 and
// sorts below U+E000 in UTF-16, while its UTF-8 bytes sort above.
func TestScopesAreSortedNotOrdered(t *testing.T) {
	f := loadFixture(t, "delegated")
	base := envelopeOf(t, f, "delegated")
	want := []any{"sandbox:\U0001F600", "sandbox:\ue000", "tools:read", "tools:write"}

	action := mustAction(t, base, f.AuthorizedArgs)
	first := action["delegation"].([]any)[0].(map[string]any)
	if got := first["scopes"].([]any); !slices.Equal(got, want) {
		t.Errorf("scopes = %q, want %q", got, want)
	}
	if got := base.GetDelegation()[0].GetScopes(); slices.Equal(got, []string{
		"sandbox:\U0001F600", "sandbox:\ue000", "tools:read", "tools:write",
	}) {
		t.Fatal("the fixture already lists its scopes in canonical order, so nothing is under test")
	}

	shuffled := proto.Clone(base).(*controlv1.ActionEnvelope)
	slices.Reverse(shuffled.Delegation[0].Scopes)
	if got, want := digestOf(t, shuffled, f.AuthorizedArgs), digestOf(t, base, f.AuthorizedArgs); got != want {
		t.Errorf("reordering scopes changed the digest: %s, want %s", got, want)
	}

	// Sorting must not deduplicate: dropping a repeat would give two documents
	// one digest, and ADR-0005 says sorted and nothing about repeats.
	repeated := proto.Clone(base).(*controlv1.ActionEnvelope)
	repeated.Delegation[0].Scopes = append(repeated.Delegation[0].Scopes, "tools:read")
	if got, want := digestOf(t, repeated, f.AuthorizedArgs), digestOf(t, base, f.AuthorizedArgs); got == want {
		t.Error("a repeated scope was silently dropped")
	}
}

// TestDigestDoesNotMutateTheEnvelope guards the sort: it runs over a slice the
// caller owns, and computing a digest may not reorder the message it is about.
func TestDigestDoesNotMutateTheEnvelope(t *testing.T) {
	f := loadFixture(t, "delegated")
	env := envelopeOf(t, f, "delegated")
	before := proto.Clone(env)
	if _, err := canon.DigestV1(env, f.AuthorizedArgs); err != nil {
		t.Fatalf("DigestV1: %v", err)
	}
	if !proto.Equal(before, env) {
		t.Error("DigestV1 changed the envelope it was given")
	}
}

func TestArgumentsBound(t *testing.T) {
	env := &controlv1.ActionEnvelope{}
	// Exactly at the limit, then one byte over it: a test on one side alone
	// passes with the bound set anywhere.
	fits := []byte(`{"a":"` + strings.Repeat("x", 65528) + `"}`)
	if len(fits) != 65536 {
		t.Fatalf("the padded document is %d bytes, want 65536", len(fits))
	}
	if _, err := canon.DigestV1(env, fits); err != nil {
		t.Errorf("65536 bytes of arguments were refused: %v", err)
	}
	over := []byte(`{"a":"` + strings.Repeat("x", 65529) + `"}`)
	_, err := canon.DigestV1(env, over)
	if !errors.Is(err, canon.ErrArgumentsTooLarge) {
		t.Errorf("65537 bytes: err = %v, want ErrArgumentsTooLarge", err)
	}
	if strings.Contains(fmt.Sprint(err), "xxxx") {
		t.Error("the refusal repeats the arguments back")
	}
}

func TestAbsentArgumentsAreTheEmptyObject(t *testing.T) {
	env := &controlv1.ActionEnvelope{}
	want := digestOf(t, env, []byte(`{}`))
	for name, args := range map[string][]byte{"nil": nil, "empty": {}} {
		if got := digestOf(t, env, args); got != want {
			t.Errorf("%s arguments = %s, want the digest of {} which is %s", name, got, want)
		}
	}
}

// TestArgumentsRefusals keeps the digest from routing around any rule the
// canonical form applies to a document. Every one of these is a document two
// implementations would canonicalize differently, or two documents one
// implementation would canonicalize the same.
func TestArgumentsRefusals(t *testing.T) {
	cases := []struct {
		name, args, want string
		sentinel         error
	}{
		{"float", `{"temperature":0.7}`, "not an integer literal", canon.ErrUnsupportedValue},
		{"integral float", `{"n":1.0}`, "not an integer literal", canon.ErrUnsupportedValue},
		{"exponent", `{"n":1e3}`, "not an integer literal", canon.ErrUnsupportedValue},
		{"integer over 2^53-1", `{"n":9007199254740992}`, "JSON-safe range", canon.ErrUnsupportedValue},
		{"integer under -(2^53-1)", `{"n":-9007199254740992}`, "JSON-safe range", canon.ErrUnsupportedValue},
		{"duplicate key", `{"amount":1,"amount":1000000}`, "duplicate object key", canon.ErrUnsupportedValue},
		{"unpaired surrogate", `{"s":"\ud800"}`, "unpaired surrogate", canon.ErrUnsupportedValue},
		{"lone low surrogate", `{"s":"\udc00"}`, "unpaired surrogate", canon.ErrUnsupportedValue},
		{"null document", `null`, "the document is null", canon.ErrUnsupportedValue},
		{"invalid UTF-8", "{\"s\":\"\xff\"}", "not valid UTF-8", nil},
		{"trailing data", `{"a":1} {"b":2}`, "trailing data", nil},
		{"not JSON", `{`, "parse", nil},
		{"nesting too deep", strings.Repeat("[", 33) + strings.Repeat("]", 33), "too deep", canon.ErrTooDeep},
	}
	env := &controlv1.ActionEnvelope{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := canon.DigestV1(env, []byte(tc.args))
			if err == nil {
				t.Fatalf("DigestV1 accepted %s", tc.name)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("err = %v, want %v", err, tc.sentinel)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to mention %q", err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "authorizedArguments") {
				t.Errorf("err = %q, want it to name the field it is about", err)
			}
		})
	}
}

// TestArgumentsDepthIsCountedInTheDocument pins the limit where the README
// states it: 32 nested containers in the arguments document, counted from the
// document's own root. The action object around it is not one of them, so a
// document the parser accepts is never refused for depth by the digest, and the
// refusal of a deeper one names the document, not the action.
func TestArgumentsDepthIsCountedInTheDocument(t *testing.T) {
	env := &controlv1.ActionEnvelope{}
	shapes := []struct {
		name, open, inner, close, step string
	}{
		{"arrays", "[", "", "]", "/0"},
		{"objects", `{"a":`, "1", "}", "/a"},
	}
	for _, s := range shapes {
		nest := func(n int) []byte {
			return []byte(strings.Repeat(s.open, n) + s.inner + strings.Repeat(s.close, n))
		}
		t.Run(s.name, func(t *testing.T) {
			at := nest(32)
			got, err := canon.DigestV1(env, at)
			if err != nil {
				t.Fatalf("32 nested %s were refused: %v", s.name, err)
			}
			// The construction holds at the limit as well: sha256 over the tag
			// and the canonical bytes of the action holding the document.
			body, err := canon.Canonicalize(mustAction(t, env, at))
			if err != nil {
				t.Fatalf("Canonicalize of the action holding 32 nested %s: %v", s.name, err)
			}
			sum := sha256.Sum256(append([]byte(actionTag), body...))
			if want := "sha256:" + hex.EncodeToString(sum[:]); got != want {
				t.Errorf("digest = %s, but sha256(tag||body) = %s", got, want)
			}

			_, err = canon.DigestV1(env, nest(33))
			if !errors.Is(err, canon.ErrTooDeep) {
				t.Fatalf("33 nested %s: err = %v, want ErrTooDeep", s.name, err)
			}
			// 32 steps into the document is where the 33rd container opens.
			want := `authorizedArguments: canon: nesting too deep at "` + strings.Repeat(s.step, 32) + `"`
			if !strings.HasPrefix(err.Error(), want) {
				t.Errorf("refusal %q, want it to start %q", err, want)
			}
		})
	}
}

// TestArgumentsAcceptsWhatTheRulesAllow is the other side of the refusals: an
// over-strict digest blocks a material action from ever being approved.
func TestArgumentsAcceptsWhatTheRulesAllow(t *testing.T) {
	env := &controlv1.ActionEnvelope{}
	for _, args := range []string{
		`{}`, `[]`, `{"nested":{"null":null}}`, `{"n":9007199254740991}`,
		`{"n":-9007199254740991}`, `{"pair":"😀"}`, `{"escaped backslash":"\\ud800"}`,
		`"a top-level string"`, `42`, `true`,
	} {
		if _, err := canon.DigestV1(env, []byte(args)); err != nil {
			t.Errorf("DigestV1 refused %s: %v", args, err)
		}
	}
}

// TestFoldedKeysAreRefusedInEveryObject covers the objects that never pass the
// parser: attributes and labels reach the canonical form as Go maps, so a check
// made only while reading JSON would leave them open (ADR-0011).
func TestFoldedKeysAreRefusedInEveryObject(t *testing.T) {
	cases := []struct {
		name, want string
		env        *controlv1.ActionEnvelope
		args       string
	}{
		{
			"principal attributes", `canon: unsupported value at "/principal/attributes"`,
			&controlv1.ActionEnvelope{Principal: &controlv1.Principal{
				Attributes: map[string]string{"Region": "eu-west", "region": "us-east"},
			}}, "",
		},
		{
			"resource labels", `canon: unsupported value at "/resource/labels"`,
			&controlv1.ActionEnvelope{Resource: &controlv1.Resource{
				Labels: map[string]string{"tier": "gold", "TIER": "bronze"},
			}}, "",
		},
		{
			"arguments", `authorizedArguments: canon: unsupported value at ""`,
			&controlv1.ActionEnvelope{}, `{"amount":1,"AMOUNT":1000000}`,
		},
		{
			"nested arguments", `authorizedArguments: canon: unsupported value at "/refund"`,
			&controlv1.ActionEnvelope{}, `{"refund":{"Amount":1,"amount":1000000}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := canon.DigestV1(tc.env, []byte(tc.args))
			if !errors.Is(err, canon.ErrUnsupportedValue) {
				t.Fatalf("err = %v, want ErrUnsupportedValue", err)
			}
			if !strings.HasPrefix(err.Error(), tc.want) || !strings.Contains(err.Error(), "folds together") {
				t.Errorf("refusal %q, want it to start %q and say the keys fold together", err, tc.want)
			}
		})
	}
}

func TestRefusesNilEnvelope(t *testing.T) {
	if _, err := canon.DigestV1(nil, nil); !errors.Is(err, canon.ErrMissingEnvelope) {
		t.Errorf("err = %v, want ErrMissingEnvelope", err)
	}
	if _, err := canon.CanonicalAction(nil, nil); !errors.Is(err, canon.ErrMissingEnvelope) {
		t.Errorf("err = %v, want ErrMissingEnvelope", err)
	}
}

// TestRefusesUndeclaredEnumNumber covers the number no name exists for.
// protojson emits such a value as a number, so accepting it would let two
// implementations disagree on the digest of one envelope.
func TestRefusesUndeclaredEnumNumber(t *testing.T) {
	cases := map[string]*controlv1.ActionEnvelope{
		"/action/effect":         {Action: &controlv1.Action{Effect: controlv1.EffectClass(99)}},
		"/destination/trustZone": {Destination: &controlv1.Destination{TrustZone: controlv1.TrustZone(77)}},
	}
	for pointer, env := range cases {
		t.Run(pointer, func(t *testing.T) {
			_, err := canon.DigestV1(env, nil)
			if !errors.Is(err, canon.ErrUnsupportedValue) {
				t.Fatalf("err = %v, want ErrUnsupportedValue", err)
			}
			if !strings.Contains(err.Error(), pointer) {
				t.Errorf("err = %q, want it to name %s", err, pointer)
			}
		})
	}
	// Every declared number still has a name, or an envelope nobody can digest
	// would validate.
	for number := range controlv1.EffectClass_name {
		env := &controlv1.ActionEnvelope{Action: &controlv1.Action{Effect: controlv1.EffectClass(number)}}
		if _, err := canon.DigestV1(env, nil); err != nil {
			t.Errorf("effect %d: %v", number, err)
		}
	}
	for number := range controlv1.TrustZone_name {
		env := &controlv1.ActionEnvelope{Destination: &controlv1.Destination{TrustZone: controlv1.TrustZone(number)}}
		if _, err := canon.DigestV1(env, nil); err != nil {
			t.Errorf("trust zone %d: %v", number, err)
		}
	}
}

// TestEnumsEncodeAsNames pins the encoding: a number changes meaning if the
// enum is ever renumbered, and the name is what protojson already emits.
func TestEnumsEncodeAsNames(t *testing.T) {
	f := loadFixture(t, "refund_prod")
	action := mustAction(t, envelopeOf(t, f, "refund_prod"), f.AuthorizedArgs)
	if got := action["action"].(map[string]any)["effect"]; got != "EFFECT_CLASS_TRANSACT" {
		t.Errorf("effect = %#v, want the name EFFECT_CLASS_TRANSACT", got)
	}
	if got := action["destination"].(map[string]any)["trustZone"]; got != "TRUST_ZONE_PARTNER" {
		t.Errorf("trustZone = %#v, want the name TRUST_ZONE_PARTNER", got)
	}
}

// bindingFixture is the on-disk shape of the approval binding golden: the
// action digest it starts from, named by fixture and written out, the bundle
// digest, and the binding. The bundle digest is a placeholder, and obviously
// one: no policy bundle exists yet.
type bindingFixture struct {
	ActionFixture   string `json:"action_fixture"`
	ActionDigest    string `json:"action_digest"`
	BundleDigest    string `json:"bundle_digest"`
	ExpectedBinding string `json:"expected_binding"`
}

// loadBindingFixture reads binding.json and checks that the action digest it
// states is the one the fixture it names pins, so the two files cannot drift
// apart without this failing.
func loadBindingFixture(t testing.TB) bindingFixture {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(fixtureDir), "binding.json")
	if err != nil {
		t.Fatalf("read the binding fixture: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f bindingFixture
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode the binding fixture: %v", err)
	}
	name, ok := strings.CutSuffix(f.ActionFixture, ".json")
	if !ok || !slices.Contains(fixtureNames, name) {
		t.Fatalf("the binding fixture starts from %q, which is not a digest fixture", f.ActionFixture)
	}
	if want := loadFixture(t, name).ExpectedDigest; f.ActionDigest != want {
		t.Fatalf("the binding fixture states action digest %s, but %s pins %s", f.ActionDigest, f.ActionFixture, want)
	}
	return f
}

// TestApprovalBindingGolden pins the second domain tag against the file other
// languages are checked against. The value is reproducible outside Go:
//
//	printf 'agent-approval-binding/v1\n%s\n%s' "$action" "$bundle" | shasum -a 256
func TestApprovalBindingGolden(t *testing.T) {
	f := loadBindingFixture(t)
	got, err := canon.ApprovalBinding(f.ActionDigest, f.BundleDigest)
	if err != nil {
		t.Fatalf("ApprovalBinding: %v", err)
	}
	assertDigestForm(t, got)

	preimage := slices.Concat([]byte(bindingTag), []byte(f.ActionDigest), []byte("\n"), []byte(f.BundleDigest))
	sum := sha256.Sum256(preimage)
	if want := "sha256:" + hex.EncodeToString(sum[:]); got != want {
		t.Errorf("binding = %s, but sha256 over the stated preimage = %s", got, want)
	}
	if f.ExpectedBinding == "" {
		t.Fatalf("fixture has no expected_binding; computed %s", got)
	}
	if got != f.ExpectedBinding {
		t.Errorf("binding = %s, want %s", got, f.ExpectedBinding)
	}
}

// TestApprovalBindingIsSensitiveToBothInputs is what stops an approval from
// being replayed against a different bundle than the approver saw, or against
// the same pair read the other way round.
func TestApprovalBindingIsSensitiveToBothInputs(t *testing.T) {
	a := loadFixture(t, "refund_prod").ExpectedDigest
	b := loadFixture(t, "mutated_amount").ExpectedDigest
	bundleDigest := loadBindingFixture(t).BundleDigest
	base := mustBind(t, a, bundleDigest)

	if got := mustBind(t, b, bundleDigest); got == base {
		t.Error("a different action digest produced the same binding")
	}
	if got := mustBind(t, a, flipLastDigit(bundleDigest)); got == base {
		t.Error("a different bundle digest produced the same binding")
	}
	if got := mustBind(t, bundleDigest, a); got == base {
		t.Error("swapping the two inputs produced the same binding")
	}
}

// flipLastDigit gives a digest one hex digit away from the one given, so the
// alternative can never coincide with what the fixture holds.
func flipLastDigit(digest string) string {
	last := "0"
	if strings.HasSuffix(digest, "0") {
		last = "1"
	}
	return digest[:len(digest)-1] + last
}

func mustBind(t testing.TB, action, bundle string) string {
	t.Helper()
	got, err := canon.ApprovalBinding(action, bundle)
	if err != nil {
		t.Fatalf("ApprovalBinding(%q, %q): %v", action, bundle, err)
	}
	return got
}

// TestApprovalBindingRefusesMalformedDigests: this is the value an approval is
// compared against, so a malformed input there is not something to hash.
func TestApprovalBindingRefusesMalformedDigests(t *testing.T) {
	good := loadFixture(t, "minimal").ExpectedDigest
	bad := map[string]string{
		"empty":            "",
		"no prefix":        strings.Repeat("a", 64),
		"wrong algorithm":  "sha512:" + strings.Repeat("a", 64),
		"upper case hex":   "sha256:" + strings.ToUpper(strings.Repeat("ab", 32)),
		"63 digits":        "sha256:" + strings.Repeat("a", 63),
		"65 digits":        "sha256:" + strings.Repeat("a", 65),
		"not hex":          "sha256:" + strings.Repeat("g", 64),
		"trailing space":   good + " ",
		"leading space":    " " + good,
		"prefix only":      "sha256:",
		"doubled prefix":   "sha256:sha256:" + strings.Repeat("a", 57),
		"non-ascii digit":  "sha256:" + strings.Repeat("a", 63) + "\u0430",
		"newline injected": good[:len(good)-1] + "\n",
	}
	for name, value := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := canon.ApprovalBinding(value, good); !errors.Is(err, canon.ErrMalformedDigest) {
				t.Errorf("as the action digest: err = %v, want ErrMalformedDigest", err)
			}
			if _, err := canon.ApprovalBinding(good, value); !errors.Is(err, canon.ErrMalformedDigest) {
				t.Errorf("as the bundle digest: err = %v, want ErrMalformedDigest", err)
			}
		})
	}
}

// TestDigestIsStableAcrossMapIteration: Go randomizes map iteration per run, so
// a digest built from attributes and labels has to sort rather than walk.
func TestDigestIsStableAcrossMapIteration(t *testing.T) {
	f := loadFixture(t, "refund_prod")
	env := envelopeOf(t, f, "refund_prod")
	want := f.ExpectedDigest
	for range 200 {
		if got := digestOf(t, env, f.AuthorizedArgs); got != want {
			t.Fatalf("digest = %s, want %s", got, want)
		}
	}
}

// interestingRunes seeds the generated strings with the characters the rules
// single out, so the property test does not spend its budget on letters. Every
// one is an escape: a literal control character in a source file is invisible.
var interestingRunes = []rune(" \"\\/~\x00\x1f\t\naA0\u00e9\u4e00\ue000\ufeff\ufffd\U0001F600\U0010FFFF")

func drawText(rt *rapid.T, label string) string {
	return rapid.OneOf(
		rapid.StringOf(rapid.SampledFrom(interestingRunes)),
		rapid.String(),
	).Draw(rt, label)
}

// TestDigestProperties drives the three properties an SDK depends on over
// generated envelopes: the digest is a function of the value, it does not
// depend on the order a set was written in, and it moves when the set changes.
func TestDigestProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		env := drawEnvelope(rt)
		args := []byte(`{"k":` + fmt.Sprint(rapid.IntRange(0, 1000).Draw(rt, "arg")) + `}`)

		first, err := canon.DigestV1(env, args)
		if err != nil {
			rt.Fatalf("DigestV1 refused a well-formed envelope: %v", err)
		}
		if !strings.HasPrefix(first, "sha256:") || len(first) != 71 {
			rt.Fatalf("digest %q is not sha256: and 64 hex digits", first)
		}
		clone := proto.Clone(env).(*controlv1.ActionEnvelope)
		again, err := canon.DigestV1(clone, args)
		if err != nil {
			rt.Fatalf("DigestV1 on a clone: %v", err)
		}
		if again != first {
			rt.Fatalf("the same action digested twice: %s then %s", first, again)
		}

		shuffled := proto.Clone(env).(*controlv1.ActionEnvelope)
		slices.Reverse(shuffled.Delegation[0].Scopes)
		got, err := canon.DigestV1(shuffled, args)
		if err != nil {
			rt.Fatalf("DigestV1 after reordering scopes: %v", err)
		}
		if got != first {
			rt.Fatalf("reordering a scope set changed the digest: %s, want %s", got, first)
		}

		changed := proto.Clone(env).(*controlv1.ActionEnvelope)
		changed.Principal.Id += " suffix"
		got, err = canon.DigestV1(changed, args)
		if err != nil {
			rt.Fatalf("DigestV1 after changing the principal: %v", err)
		}
		if got == first {
			rt.Fatalf("a changed principal kept the digest %s", first)
		}
	})
}

func drawEnvelope(rt *rapid.T) *controlv1.ActionEnvelope {
	scopes := rapid.SliceOfN(rapid.StringOf(rapid.SampledFrom(interestingRunes)), 2, 6).Draw(rt, "scopes")
	return &controlv1.ActionEnvelope{
		// Excluded fields are drawn too, so a leak into the digest shows up as
		// a failure of the clone property rather than never being exercised.
		RequestId:  drawText(rt, "request_id"),
		TraceId:    drawText(rt, "trace_id"),
		OccurredAt: timestamppb.New(clock()),
		Principal: &controlv1.Principal{
			Id:         drawText(rt, "principal_id"),
			Type:       drawText(rt, "principal_type"),
			Attributes: map[string]string{drawText(rt, "attr_key"): drawText(rt, "attr_value")},
		},
		Agent: &controlv1.Agent{Id: drawText(rt, "agent_id")},
		Delegation: []*controlv1.Delegation{{
			From:   drawText(rt, "from"),
			To:     drawText(rt, "to"),
			Scopes: scopes,
		}},
		Action: &controlv1.Action{
			Name:   drawText(rt, "action_name"),
			Effect: controlv1.EffectClass(rapid.Int32Range(0, 9).Draw(rt, "effect")),
		},
		Resource:    &controlv1.Resource{Type: drawText(rt, "resource_type")},
		Destination: &controlv1.Destination{Host: drawText(rt, "host")},
	}
}

// TestReadmeListsEveryGolden keeps the page an implementation in another
// language reads from drifting away from the files beside it. A digest the
// README states and no fixture holds is worse than no README.
func TestReadmeListsEveryGolden(t *testing.T) {
	page, err := fs.ReadFile(os.DirFS(fixtureDir), "README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, name := range fixtureNames {
		f := loadFixture(t, name)
		if n := strings.Count(string(page), f.ExpectedDigest); n != 1 {
			t.Errorf("README names the %s digest %d times, want once", name, n)
		}
		if !strings.Contains(string(page), name+".json") {
			t.Errorf("README does not name the file %s.json", name)
		}
	}
	hash := loadArgumentsFixture(t).ExpectedHash
	if n := strings.Count(string(page), hash); n != 1 {
		t.Errorf("README names the arguments hash %d times, want once", n)
	}
	if !strings.Contains(string(page), "arguments_hash.json") {
		t.Error("README does not name the file arguments_hash.json")
	}
	binding := loadBindingFixture(t).ExpectedBinding
	if n := strings.Count(string(page), binding); n != 1 {
		t.Errorf("README names the approval binding %d times, want once", n)
	}
	if !strings.Contains(string(page), "binding.json") {
		t.Error("README does not name the file binding.json")
	}
	for _, must := range []string{
		actionTag[:len(actionTag)-1], bindingTag[:len(bindingTag)-1], argumentsTag[:len(argumentsTag)-1], "65536",
	} {
		if !strings.Contains(string(page), must) {
			t.Errorf("README does not state %q", must)
		}
	}
}

// TestEveryFixtureFileIsRead: a .json file placed beside the contract that no
// test reads is a golden nothing pins, so the directory holds only the digest
// fixtures, the arguments hash and the approval binding.
func TestEveryFixtureFileIsRead(t *testing.T) {
	entries, err := fs.ReadDir(os.DirFS(fixtureDir), ".")
	if err != nil {
		t.Fatalf("read the fixture directory: %v", err)
	}
	known := []string{"arguments_hash.json", "binding.json"}
	for _, name := range fixtureNames {
		known = append(known, name+".json")
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if !slices.Contains(known, entry.Name()) {
			t.Errorf("%s is not a fixture any test reads", entry.Name())
		}
	}
}

// TestNoStringIsNormalized closes the gap a mutation found: nothing trims,
// case-folds or Unicode-normalizes a value on its way into the digest. An
// implementation that does computes a different digest for the same action, and
// the mismatch reads as tampering rather than as a normalization difference.
// RFC 8785 does not normalize either, so this is the canonical form's rule as
// much as this file's.
func TestNoStringIsNormalized(t *testing.T) {
	setters := map[string]func(*controlv1.ActionEnvelope, string){
		"/principal/id":         func(e *controlv1.ActionEnvelope, v string) { e.Principal.Id = v },
		"/principal/attributes": func(e *controlv1.ActionEnvelope, v string) { e.Principal.Attributes = map[string]string{"k": v} },
		"/principal/attributes key": func(e *controlv1.ActionEnvelope, v string) {
			e.Principal.Attributes = map[string]string{v: "v"}
		},
		"/agent/id":            func(e *controlv1.ActionEnvelope, v string) { e.Agent.Id = v },
		"/delegation/0/from":   func(e *controlv1.ActionEnvelope, v string) { e.Delegation[0].From = v },
		"/delegation/0/scopes": func(e *controlv1.ActionEnvelope, v string) { e.Delegation[0].Scopes = []string{v} },
		"/action/name":         func(e *controlv1.ActionEnvelope, v string) { e.Action.Name = v },
		"/resource/id":         func(e *controlv1.ActionEnvelope, v string) { e.Resource.Id = v },
		"/destination/host":    func(e *controlv1.ActionEnvelope, v string) { e.Destination.Host = v },
	}
	// Each pair is two spellings a producer might treat as one value.
	pairs := []struct{ name, a, b string }{
		{"surrounding space", "x-1", " x-1 "},
		{"inner space", "x 1", "x  1"},
		{"case", "x-1", "X-1"},
		{"trailing dot", "host.test", "host.test."},
		{"NFC and NFD", "caf\u00e9", "cafe\u0301"},
		{"zero width space", "x-1", "x-1\u200b"},
	}
	f := loadFixture(t, "refund_prod")
	base := envelopeOf(t, f, "refund_prod")

	for pointer, set := range setters {
		for _, pair := range pairs {
			t.Run(pointer+"/"+pair.name, func(t *testing.T) {
				first := proto.Clone(base).(*controlv1.ActionEnvelope)
				second := proto.Clone(base).(*controlv1.ActionEnvelope)
				set(first, pair.a)
				set(second, pair.b)
				if digestOf(t, first, f.AuthorizedArgs) == digestOf(t, second, f.AuthorizedArgs) {
					t.Errorf("%q and %q produced one digest", pair.a, pair.b)
				}
			})
		}
	}
}

// FuzzDigestArguments drives the arguments document from raw bytes, which is
// where a panic would live: the digest reads whatever an adapter hands it, and
// a crash there stops an enforcement decision from happening rather than
// refusing it.
//
// The property is the one an implementation in another language depends on:
// the digest is a function of the value, so digesting a document and digesting
// its canonical form give the same answer.
func FuzzDigestArguments(f *testing.F) {
	for _, seed := range []string{
		`{}`, `[]`, `null`, `0`, `""`, `{"a":1}`, `{"a":1,"a":2}`, `{"a":0.5}`,
		`{"a":"\ud800"}`, `{"a":"😀"}`, `{"a":9007199254740992}`,
		`{"b":{"c":[true,false,null]}}`, ` { "a" : 1 } `, `{`,
	} {
		f.Add([]byte(seed))
	}
	fixture := loadFixture(f, "refund_prod")
	env := envelopeOf(f, fixture, "refund_prod")

	f.Fuzz(func(t *testing.T, args []byte) {
		got, err := canon.DigestV1(env, args)
		if err != nil {
			if got != "" {
				t.Errorf("refused %q but returned the digest %q", args, got)
			}
			return
		}
		assertDigestForm(t, got)
		if len(args) == 0 {
			return
		}
		body, err := canon.CanonicalizeJSON(args)
		if err != nil {
			t.Fatalf("DigestV1 accepted arguments CanonicalizeJSON refuses: %v", err)
		}
		again, err := canon.DigestV1(env, body)
		if err != nil {
			t.Fatalf("DigestV1 refused the canonical form of arguments it accepted: %v", err)
		}
		if again != got {
			t.Errorf("digest depends on the spelling: %s for %q, %s for %q", got, args, again, body)
		}
	})
}
