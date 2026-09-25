package contract_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// TestCheckMapKeyIsValidatesKeyRule holds the exported check to Validate, as
// docs/contracts.md promises under "Decoding and validation". Validate's
// verdict on a key standing alone in any map of the envelope is the string
// bound and CheckMapKey, nothing else, and where CheckMapKey refuses, Validate
// refuses for the same reason: at the entry when the key takes the reserved
// namespace, at its key when the spelling is refused. Two keys that fold
// together are a relation between keys, which CheckMapKey does not see and
// Validate keeps. The keys are built from the prefix, its foldings and their
// neighbours, so that the ninth code point and the fold are both drawn.
func TestCheckMapKeyIsValidatesKeyRule(t *testing.T) {
	pieces := []string{
		"reserved.", "Reserved.", "RESERVED", "re\U0000017ferved.", "reserve", "d", "x", ".", "owner",
		"\U0000212a", "\U0000200b", " ", "\U000000ad", "\U0000fe0f", "\xff",
	}
	rapid.Check(t, func(rt *rapid.T) {
		key := rapid.OneOf(
			rapid.Map(rapid.SliceOfN(rapid.SampledFrom(pieces), 0, 6), func(p []string) string { return strings.Join(p, "") }),
			rapid.String(),
			rapid.Map(rapid.SliceOf(rapid.Byte()), func(b []byte) string { return string(b) }),
		).Draw(rt, "key")
		for _, m := range stringMaps() {
			assertMapKeyAgreement(rt, m.path, m.fill, key)
		}
	})

	// The bound, which no draw above reaches, and the empty key, which both
	// accept: whether a key is required is the caller's question.
	for _, key := range []string{"", strings.Repeat("k", contract.MaxStringBytes), strings.Repeat("k", contract.MaxStringBytes+1)} {
		for _, m := range stringMaps() {
			assertMapKeyAgreement(t, m.path, m.fill, key)
		}
	}
}

// assertMapKeyAgreement holds Validate's verdict on one key alone in the map at
// path to the bound and CheckMapKey, and where CheckMapKey refuses on its own,
// holds Validate to CheckMapKey's reason, at the entry or at its key.
func assertMapKeyAgreement(t rapid.TB, path string, fill func(*controlv1.ActionEnvelope, ...string), key string) {
	t.Helper()
	env := valid()
	fill(env, key)
	got := contract.Validate(env)
	ours := contract.CheckMapKey(key)
	want := len(key) <= contract.MaxStringBytes && ours == nil
	if (got == nil) != want {
		t.Fatalf("%s: Validate accepts key %+q: %v; the bound and CheckMapKey accept it: %v (Validate: %v, CheckMapKey: %v)",
			path, key, got == nil, want, got, ours)
	}
	if ours == nil || len(key) > contract.MaxStringBytes {
		return
	}
	field := path + "[0]"
	if contract.CheckIdentifier(key) != nil {
		field += ".key"
	}
	var theirs, mine *contract.ValidationError
	if !errors.As(got, &theirs) || !errors.As(ours, &mine) {
		t.Fatalf("%s: key %+q: Validate = %v and CheckMapKey = %v, not both *ValidationError", path, key, got, ours)
	}
	if theirs.Field != field || mine.Field != "" {
		t.Fatalf("%s: key %+q: Validate names %q, CheckMapKey names %q; want %q and no field", path, key, theirs.Field, mine.Field, field)
	}
	if theirs.Err.Error() != mine.Err.Error() {
		t.Fatalf("%s: key %+q: two reasons for one refusal: Validate %q, CheckMapKey %q", path, key, theirs.Err, mine.Err)
	}
}

// TestCheckMapKeyRefusalNamesNoFieldAndNoKey: the refusal is a *ValidationError
// with no Field, wrapping ErrInvalidValue, one printable line that never
// repeats the key, which is the caller's.
func TestCheckMapKeyRefusalNamesNoFieldAndNoKey(t *testing.T) {
	const marker = "hunter2"
	for _, key := range []string{
		"reserved." + marker, "Reserved." + marker, marker + " ", marker + "\U0000200b", marker + "\xff",
	} {
		err := contract.CheckMapKey(key)
		if err == nil {
			t.Fatalf("CheckMapKey(%+q) = nil, so this case proves nothing", key)
		}
		assertRefused(t, err, contract.ErrInvalidValue, "")
		if strings.Contains(err.Error(), marker) {
			t.Errorf("the refusal repeats the caller's key: %v", err)
		}
		for _, r := range err.Error() {
			if !unicode.IsPrint(r) {
				t.Errorf("the refusal %+q holds %U", err.Error(), r)
			}
		}
	}
}
