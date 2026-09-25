package contract_test

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"pgregory.net/rapid"

	"github.com/guardana/control/pkg/contract"
)

// TestUnicodeVersionIsPinned: Cf and Default_Ignorable_Code_Point change
// between Unicode versions, and Go's tables move with the toolchain. ADR-0011
// states the rule at 17.0.0, so an upgrade that moves them fails here rather
// than changing what Validate refuses without anyone deciding it.
func TestUnicodeVersionIsPinned(t *testing.T) {
	if unicode.Version != "17.0.0" {
		t.Fatalf("unicode.Version is %s and ADR-0011 states the identifier rule at 17.0.0: compare the refused sets, record the new version, then change this line",
			unicode.Version)
	}
}

func TestCheckIdentifierRefusesEveryClass(t *testing.T) {
	for _, v := range identifierRefusals() {
		t.Run(v.name, func(t *testing.T) {
			assertRefused(t, contract.CheckIdentifier(v.value), contract.ErrInvalidValue, "")
		})
	}
}

// TestCheckIdentifierAcceptsWhatTheRuleDoesNotName: the rule is a list of
// classes, not "printable ASCII". The last two rows are accepted because
// ADR-0011 names neither class; the evidence builder refuses the first today
// (see TestCheckIdentifierAgainstTheBuilderRule), and the second renders as
// nothing without being default-ignorable, so no rule catches it.
func TestCheckIdentifierAcceptsWhatTheRuleDoesNotName(t *testing.T) {
	for _, v := range []namedString{
		{"the empty string: presence is the caller's to check", ""},
		{"ascii", "req-1"},
		{"a space inside", "req 1"},
		{"latin with a diacritic", "Z\U000000fcrich"},
		{"cjk", "\U00006771\U00004eac"},
		{"an emoji", "\U0001F642"},
		{"a decomposed accent, which nothing normalises", "e\U00000301"},
		{"a combining mark inside", "a\U00000301b"},
		{"private use U+E000", "a\U0000e000b"},
		{"braille pattern blank U+2800", "a\U00002800b"},
	} {
		if err := contract.CheckIdentifier(v.value); err != nil {
			t.Errorf("%s: CheckIdentifier(%+q) = %v, want nil", v.name, v.value, err)
		}
	}
}

// TestCheckIdentifierRefusesWhatTheBuilderRefuses: every identifier
// internal/evidence's builder refused before it used this function is refused
// here, and by Validate as a request_id, so no valid envelope is one the
// builder cannot record. The vectors are copied as literals from the builder's
// own tests.
func TestCheckIdentifierRefusesWhatTheBuilderRefuses(t *testing.T) {
	for _, s := range []string{
		" req-1 ", "req-1\t", "\U0000200b", "req-\U0000feff" + "1", "req-\x00" + "1", "req\U000000ad" + "1",
		"req\U00002060" + "1", "req\U0000180e" + "1", "req\U0000200e" + "1", "req-\xff\xfe", "   ",
		"run\U0000200b" + "1",
		// Padding, an invisible rune and a bidi override inside a request id.
		"req-1 ", " req-1", "req\U0000200b" + "1", "req\U0000202e" + "1",
	} {
		if err := contract.CheckIdentifier(s); !errors.Is(err, contract.ErrInvalidValue) {
			t.Errorf("CheckIdentifier(%+q) = %v, want ErrInvalidValue", s, err)
		}
		e := valid()
		e.RequestId = s
		if err := contract.Validate(e); !errors.Is(err, contract.ErrInvalidValue) {
			t.Errorf("Validate with request_id %+q = %v, want ErrInvalidValue", s, err)
		}
	}
}

// TestCheckIdentifierRefusalCarriesNoCallerText: the refusal names a class and
// a code point by number, never the string, and renders as one printable line.
func TestCheckIdentifierRefusalCarriesNoCallerText(t *testing.T) {
	const marker = "sk_live_HUNTER2"
	for _, s := range []string{
		marker + "\U0000200b", " " + marker, marker + "\x00", marker + "\xff", marker + "\U00002028",
	} {
		err := contract.CheckIdentifier(s)
		if err == nil {
			t.Fatalf("CheckIdentifier(%+q) = nil, so this case proves nothing", s)
		}
		if strings.Contains(err.Error(), marker) {
			t.Errorf("the refusal repeats the caller's string: %v", err)
		}
		for _, r := range err.Error() {
			if !unicode.IsPrint(r) {
				t.Errorf("the refusal %+q holds %U", err.Error(), r)
			}
		}
	}
}

// TestIgnorableInputsHoldNoWhiteSpace pins the equivalence refusedClass rests
// on: refusing Cf, Other_Default_Ignorable_Code_Point and Variation_Selector is
// refusing Cf and Default_Ignorable_Code_Point only while no White_Space code
// point is in the last two, because the derivation subtracts White_Space.
func TestIgnorableInputsHoldNoWhiteSpace(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !unicode.Is(unicode.White_Space, r) {
			continue
		}
		if unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r) {
			t.Errorf("U+%04X is White_Space and an input of Default_Ignorable_Code_Point; it would be refused inside a string, where the rule allows it", r)
		}
	}
}

// builderRefuses is the identifier rule internal/evidence's builder kept
// before it took the contract's: trimmed, valid UTF-8, every rune printable.
// Copied rather than imported: this package may not import that one, and a
// copy is what fails when either side moves.
func builderRefuses(s string) bool {
	if !utf8.ValidString(s) || s != strings.TrimSpace(s) {
		return true
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

// TestCheckIdentifierAgainstTheBuilderRule states the difference between the
// two rules, both ways, over strings nobody chose.
//
// The builder refuses and this rule accepts: a private-use code point, an
// unassigned one. ADR-0011 names neither, so moving the builder to
// CheckIdentifier accepts them there. A space other than U+0020 inside the
// string was on this side until ADR-0011's amendment; both refuse it now.
// This rule refuses and the builder accepts: a default-ignorable code point
// that unicode.IsPrint counts as graphic, U+034F, U+115F, U+3164, the
// variation selectors.
func TestCheckIdentifierAgainstTheBuilderRule(t *testing.T) {
	interesting := []rune{
		'a', 'Z', '0', '-', ' ', '\t', '\n', 0x00, 0x7f, 0x85, 0xa0, 0xad, 0x301, 0x34f, 0x378, 0x115f,
		0x1680, 0x180e, 0x2000, 0x200b, 0x200d, 0x2028, 0x202e, 0x2065, 0x2800, 0x3000, 0x3164,
		0x6771, 0xe000, 0xfe0f, 0xfeff, 0xffa0, 0xfff9, 0x1f642, 0xe0001, 0xe0100, 0x10ffff,
	}
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.OneOf(
			rapid.String(),
			rapid.StringOf(rapid.SampledFrom(interesting)),
			rapid.Map(rapid.SliceOf(rapid.Byte()), func(b []byte) string { return string(b) }),
		).Draw(rt, "s")

		ours, theirs := contract.CheckIdentifier(s) != nil, builderRefuses(s)
		switch {
		case theirs && !ours && !hasRune(s, onlyTheBuilderRefuses):
			rt.Fatalf("the builder refuses %+q and CheckIdentifier accepts it, outside the stated difference", s)
		case ours && !theirs && !hasRune(s, onlyThisRuleRefuses):
			rt.Fatalf("CheckIdentifier refuses %+q and the builder accepts it, outside the stated difference", s)
		}
	})
}

// onlyTheBuilderRefuses names the categories one at a time: Go's unicode.C
// also holds every unassigned code point, so "in no category" cannot be
// written with it. A space other than U+0020 is not on it: ADR-0011's
// amendment made this rule refuse one anywhere, as the builder does.
func onlyTheBuilderRefuses(r rune) bool {
	assigned := unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z,
		unicode.Cc, unicode.Cf, unicode.Co, unicode.Cs)
	return unicode.Is(unicode.Co, r) || !assigned
}

func onlyThisRuleRefuses(r rune) bool {
	ignorable := unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) || unicode.Is(unicode.Variation_Selector, r)
	return ignorable && unicode.IsPrint(r)
}

func hasRune(s string, match func(rune) bool) bool {
	for _, r := range s {
		if match(r) {
			return true
		}
	}
	return false
}
