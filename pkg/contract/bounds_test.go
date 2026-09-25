package contract_test

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// A bound no test holds at its edge can be deleted or narrowed with every test
// green. Each test here is the input at which a bound changes the answer, on
// both sides of it.

func TestValidateBoundsAMapKeyAndAMapValue(t *testing.T) {
	at, over := strings.Repeat("k", contract.MaxStringBytes), strings.Repeat("k", contract.MaxStringBytes+1)

	env := valid()
	env.Principal.Attributes = map[string]string{at: "v"}
	if err := contract.Validate(env); err != nil {
		t.Errorf("a %d-byte key is refused: %v", len(at), err)
	}
	env.Principal.Attributes = map[string]string{over: "v"}
	assertRefused(t, contract.Validate(env), contract.ErrTooLarge, "principal.attributes[0].key")

	env = valid()
	env.Resource.Labels = map[string]string{"k": at}
	if err := contract.Validate(env); err != nil {
		t.Errorf("a %d-byte value is refused: %v", len(at), err)
	}
	env.Resource.Labels = map[string]string{"k": over}
	assertRefused(t, contract.Validate(env), contract.ErrTooLarge, "resource.labels[0]")
}

// TestValidateGivesThePreviewAloneTheLargerLimit: every other Arguments string
// is held to MaxStringBytes. At its limit canonical_hash is refused for its
// shape, not its length, which is what separates the two limits for it.
func TestValidateGivesThePreviewAloneTheLargerLimit(t *testing.T) {
	for _, tc := range []struct {
		path    string
		limit   int
		atLimit error
		set     func(*controlv1.Arguments, string)
	}{
		{"arguments.redacted_preview", contract.MaxPreviewBytes, nil, func(a *controlv1.Arguments, s string) { a.RedactedPreview = s }},
		{"arguments.schema_ref", contract.MaxStringBytes, nil, func(a *controlv1.Arguments, s string) { a.SchemaRef = s }},
		{"arguments.redaction_profile", contract.MaxStringBytes, nil, func(a *controlv1.Arguments, s string) { a.RedactionProfile = s }},
		{"arguments.canonical_hash", contract.MaxStringBytes, contract.ErrInvalidValue, func(a *controlv1.Arguments, s string) { a.CanonicalHash = s }},
	} {
		t.Run(tc.path, func(t *testing.T) {
			build := func(n int) *controlv1.ActionEnvelope {
				env := valid()
				env.Arguments = &controlv1.Arguments{RedactionProfile: "default"}
				tc.set(env.Arguments, strings.Repeat("a", n))
				return env
			}
			err := contract.Validate(build(tc.limit))
			switch {
			case tc.atLimit != nil:
				assertRefused(t, err, tc.atLimit, tc.path)
			case err != nil:
				t.Errorf("%d bytes are refused: %v", tc.limit, err)
			}
			assertRefused(t, contract.Validate(build(tc.limit+1)), contract.ErrTooLarge, tc.path)
		})
	}
}

// TestTheEnvelopeBoundIsInclusive: exactly MaxEnvelopeBytes is accepted on
// every path and one more is refused. The in-memory case over the bound is the
// one input only Validate's own size check refuses, every string and
// collection being inside its limit.
func TestTheEnvelopeBoundIsInclusive(t *testing.T) {
	at := ofSize(t, contract.MaxEnvelopeBytes)
	if err := contract.Validate(at); err != nil {
		t.Errorf("an envelope of exactly %d bytes is refused: %v", contract.MaxEnvelopeBytes, err)
	}
	if _, err := contract.Decode(marshalled(t, at)); err != nil {
		t.Errorf("Decode refuses exactly %d bytes: %v", contract.MaxEnvelopeBytes, err)
	}

	over := ofSize(t, contract.MaxEnvelopeBytes+1)
	assertRefused(t, contract.Validate(over), contract.ErrTooLarge, "")
	_, err := contract.Decode(marshalled(t, over))
	assertRefused(t, err, contract.ErrTooLarge, "")

	document, err := protojson.Marshal(valid())
	if err != nil {
		t.Fatalf("protojson: %v", err)
	}
	// White space before the closing brace, which JSON allows anywhere between
	// tokens.
	padded := func(n int) []byte {
		return concat(document[:len(document)-1], bytes.Repeat([]byte(" "), n-len(document)), []byte("}"))
	}
	if _, err := contract.DecodeJSON(padded(contract.MaxEnvelopeBytes)); err != nil {
		t.Errorf("DecodeJSON refuses a document of exactly %d bytes: %v", contract.MaxEnvelopeBytes, err)
	}
	_, err = contract.DecodeJSON(padded(contract.MaxEnvelopeBytes + 1))
	assertRefused(t, err, contract.ErrTooLarge, "")
}

// ofSize returns an envelope Validate accepts but for its size, encoded in
// exactly n bytes, n within a few KB of MaxEnvelopeBytes: eight hops of 32
// full-length scopes, with the last scopes cut until the size is right.
// Between 128 and 16383 bytes a string's length prefix is two bytes, so each
// byte cut is one byte less on the wire.
func ofSize(t *testing.T, n int) *controlv1.ActionEnvelope {
	t.Helper()
	env := withDelegation(contract.MaxDelegationDepth)
	for _, hop := range env.Delegation {
		for i := range contract.MaxLabels {
			prefix := strconv.Itoa(i)
			hop.Scopes = append(hop.Scopes, prefix+strings.Repeat("s", contract.MaxStringBytes-len(prefix)))
		}
	}
	excess := proto.Size(env) - n
	for h := len(env.Delegation) - 1; h >= 0 && excess > 0; h-- {
		scopes := env.Delegation[h].Scopes
		for i := len(scopes) - 1; i >= 0 && excess > 0; i-- {
			cut := min(excess, len(scopes[i])-128)
			scopes[i] = scopes[i][:len(scopes[i])-cut]
			excess -= cut
		}
	}
	if size, wire := proto.Size(env), len(marshalled(t, env)); size != n || wire != n {
		t.Fatalf("built an envelope of %d bytes (%d encoded), want %d", size, wire, n)
	}
	return env
}
