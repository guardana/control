package canon

import (
	"errors"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"pgregory.net/rapid"
)

// TestUnicodeVersionIsPinned: which keys fold together depends on the Unicode
// tables the toolchain ships, and ADR-0011 fixes the rule at 17.0.0. A toolchain
// upgrade that moves the version turns this red instead of silently changing
// what the digest refuses.
func TestUnicodeVersionIsPinned(t *testing.T) {
	t.Parallel()
	if unicode.Version != "17.0.0" {
		t.Fatalf("unicode.Version = %s; the fold rule is fixed at Unicode 17.0.0 (ADR-0011)", unicode.Version)
	}
}

// TestFoldKeyIsEqualFold checks foldRune against strings.EqualFold over every
// code point: each maps to a point it folds together with, and a point that
// folds with no other maps to itself. EqualFold compares strings code point by
// code point, so two keys then share a fold key exactly when EqualFold says
// they are equal.
func TestFoldKeyIsEqualFold(t *testing.T) {
	t.Parallel()

	folding := 0
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue // a surrogate, which no valid UTF-8 key holds
		}
		f := foldRune(r)
		if !strings.EqualFold(string(r), string(f)) {
			t.Fatalf("foldRune(%U) = %U, which does not fold together with it", r, f)
		}
		if unicode.SimpleFold(r) == r {
			if f != r {
				t.Fatalf("foldRune(%U) = %U, but %U folds together with nothing", r, f, r)
			}
			continue
		}
		folding++
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if g := foldRune(next); g != f {
				t.Fatalf("%U and %U fold together but map to %U and %U", r, next, f, g)
			}
		}
	}
	// A loop that never reached a folding code point would pass everything.
	if folding < 2000 {
		t.Fatalf("only %d code points fold together with another", folding)
	}
}

// TestFoldKeyJoinsEverySpellingOfAName: ASCII case, U+017F LATIN SMALL LETTER
// LONG S, which folds with s, and U+212A KELVIN SIGN, which folds with k, all
// give one key; a name that differs in a letter does not.
func TestFoldKeyJoinsEverySpellingOfAName(t *testing.T) {
	t.Parallel()
	for _, pair := range [][2]string{{"ssn", "SSN"}, {"ssn", "Ssn"}, {"ssn", "\u017fsn"}, {"key", "\u212aey"}, {"key", "KEY"}} {
		if FoldKey(pair[0]) != FoldKey(pair[1]) {
			t.Errorf("FoldKey(%q) = %q and FoldKey(%q) = %q; want one key", pair[0], FoldKey(pair[0]), pair[1], FoldKey(pair[1]))
		}
	}
	if FoldKey("ssn") == FoldKey("sn") || FoldKey("ssn") == FoldKey("ssm") {
		t.Errorf("FoldKey joins names that differ in a letter")
	}
}

// TestFoldIsCheckedAfterUnescaping: a key written as an escape is the same key,
// so the rule reads decoded names, as the duplicate rule does.
func TestFoldIsCheckedAfterUnescaping(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{`{"k":1,"\u212a":2}`, `{"\u0041":1,"a":2}`, `{"s":1,"\u017f":2}`} {
		// 0x5c is the reverse solidus. A document with its escape typed out as
		// the character tests nothing about unescaping, and looks the same.
		if !strings.ContainsRune(doc, 0x5c) {
			t.Fatalf("%s holds no escape", doc)
		}
		if _, err := CanonicalizeJSON([]byte(doc)); !errors.Is(err, ErrUnsupportedValue) {
			t.Errorf("CanonicalizeJSON(%s): err = %v, want the fold refusal", doc, err)
		}
	}
}

// foldedKeyCases pairs two member names. fold is read from CaseFolding.txt
// 17.0.0 by hand, statuses C and S only, and written out rather than computed,
// so the table can disagree with the implementation and with the standard
// library alike. Every character outside ASCII is an escape: several of these
// look identical to an ASCII letter.
var foldedKeyCases = []struct {
	name string
	a, b string
	fold bool
}{
	{"ascii case", "amount", "AMOUNT", true},
	{"mixed case", "Amount", "aMOUNT", true},
	{"kelvin sign and k", "k", "\u212a", true},
	{"kelvin sign and K", "K", "\u212a", true},
	{"long s and s", "s", "\u017f", true},
	{"final sigma and sigma", "\u03c3", "\u03c2", true},
	{"capital sigma and final sigma", "\u03a3", "\u03c2", true},
	{"micro sign and mu", "\u00b5", "\u03bc", true},
	{"capital sharp s and sharp s", "\u1e9e", "\u00df", true},
	{"titlecase and lowercase dz", "\u01c5", "\u01c6", true},
	{"ohm sign and omega", "\u2126", "\u03c9", true},
	{"angstrom sign and a with ring", "\u212b", "\u00e5", true},
	{"theta symbol and theta", "\u03d1", "\u03b8", true},
	{"astral deseret pair", "\U00010400", "\U00010428", true},
	{"fold inside a longer key", "refund_Amount", "refund_amount", true},
	// Full folding (status F) would equate these; simple folding does not.
	{"sharp s is not ss", "\u00df", "ss", false},
	{"ligature ff is not ff", "\ufb00", "ff", false},
	// Case mapping, not folding: a decoder comparing through upper case
	// equates these, and the rule does not defend it.
	{"dotless i is not I", "\u0131", "I", false},
	{"dotted capital I is not i", "\u0130", "i", false},
	{"different letters", "a", "b", false},
	{"different lengths", "k", "kk", false},
	{"no normalization either", "\u00e9", "e\u0301", false},
}

func TestFoldedKeysAreRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range foldedKeyCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := strings.EqualFold(tc.a, tc.b); got != tc.fold {
				t.Fatalf("strings.EqualFold(%q, %q) = %v, but CaseFolding.txt reads %v", tc.a, tc.b, got, tc.fold)
			}
			_, parsed := CanonicalizeJSON([]byte(`{"` + tc.a + `":1,"` + tc.b + `":2}`))
			_, encoded := Canonicalize(map[string]any{tc.a: 1, tc.b: 2})
			for entry, err := range map[string]error{"CanonicalizeJSON": parsed, "Canonicalize": encoded} {
				switch {
				case !tc.fold && err != nil:
					t.Errorf("%s refused %q beside %q: %v", entry, tc.a, tc.b, err)
				case tc.fold && !errors.Is(err, ErrUnsupportedValue):
					t.Errorf("%s(%q beside %q): err = %v, want ErrUnsupportedValue", entry, tc.a, tc.b, err)
				case tc.fold && !strings.Contains(err.Error(), "folds together"):
					t.Errorf("%s: refusal %q does not say the keys fold together", entry, err)
				}
			}
		})
	}
}

// TestFoldedKeyRefusalNamesPositions: like a duplicate, the refusal names the
// object and the members by position, never by name, because a member name is
// caller content. The parser counts in document order; the encoder, which is
// handed a Go map with no order of its own, in canonical order.
func TestFoldedKeyRefusalNamesPositions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fail func() error
		want []string
	}{
		{
			"parser, document order",
			func() error { _, err := CanonicalizeJSON([]byte(`{"b":1,"a":2,"A":3}`)); return err },
			[]string{`at ""`, "member 2 folds together with member 1"},
		},
		{
			"parser, nested object",
			func() error {
				_, err := CanonicalizeJSON([]byte(`{"args":{"x":[{"Token":1,"TOKEN":2}]}}`))
				return err
			},
			[]string{`at "/args/x/0"`, "member 1 folds together with member 0"},
		},
		{
			"encoder, canonical order",
			func() error { _, err := Canonicalize(map[string]any{"b": 1, "a": 2, "A": 3}); return err },
			[]string{`at ""`, "member 1 folds together with member 0"},
		},
		{
			"encoder, nested map",
			func() error {
				_, err := Canonicalize(map[string]any{"x": map[string]any{"K": 1, "\u212a": 2}})
				return err
			},
			[]string{`at "/x"`, "member 1 folds together with member 0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.fail()
			if !errors.Is(err, ErrUnsupportedValue) {
				t.Fatalf("err = %v, want ErrUnsupportedValue", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not carry %q", err, want)
				}
			}
		})
	}

	const secret = "SECRET-MEMBER-NAME"
	for entry, fail := range map[string]func() error{
		"parser": func() error {
			_, err := CanonicalizeJSON([]byte(`{"` + secret + `":1,"` + strings.ToLower(secret) + `":2}`))
			return err
		},
		"encoder": func() error {
			_, err := Canonicalize(map[string]any{secret: 1, strings.ToLower(secret): 2})
			return err
		},
	} {
		err := fail()
		if err == nil {
			t.Fatalf("%s accepted two keys that fold together", entry)
		}
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(secret)) {
			t.Errorf("%s refusal %q repeats a member name", entry, err)
		}
	}
}

// foldAlphabet holds the characters whose folding is irregular: letters that
// fold across scripts, a titlecase triple, an astral pair, and the characters
// case mapping equates and simple folding does not.
var foldAlphabet = []rune("aAkKsSiI_1" +
	"\u212a\u017f\u00df\u1e9e\u03c3\u03c2\u03a3\u00b5\u03bc\u039c" +
	"\u0131\u0130\u01c4\u01c5\u01c6\u2126\u03c9\u03a9\u212b\u00e5\u00c5" +
	"\u03d1\u03b8\u0398\u03f4\U00010400\U00010428")

// TestFoldRefusalAgreesWithEqualFold holds both entry points to the standard
// library's statement of simple case folding, which is the relation ADR-0011
// names: two distinct keys are refused exactly when strings.EqualFold says they
// are equal.
func TestFoldRefusalAgreesWithEqualFold(t *testing.T) {
	t.Parallel()

	key := rapid.StringOfN(rapid.SampledFrom(foldAlphabet), 1, 3, -1)
	rapid.Check(t, func(rt *rapid.T) {
		a, b := key.Draw(rt, "a"), key.Draw(rt, "b")
		if a == b {
			return
		}
		want := strings.EqualFold(a, b)
		_, encoded := Canonicalize(map[string]any{a: 1, b: 2})
		if (encoded != nil) != want {
			rt.Fatalf("Canonicalize with keys %q and %q: err = %v, EqualFold = %v", a, b, encoded, want)
		}
		_, parsed := CanonicalizeJSON([]byte(`{"` + a + `":1,"` + b + `":2}`))
		if (parsed != nil) != want {
			rt.Fatalf("CanonicalizeJSON with keys %q and %q: err = %v, EqualFold = %v", a, b, parsed, want)
		}
	})
}
