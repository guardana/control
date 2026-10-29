package approval_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"pgregory.net/rapid"

	"github.com/guardana/control/internal/core/approval"
	"github.com/guardana/control/pkg/contract"
)

// reservedSpellings are claimed ids a reader takes for "authn:" followed by
// something. Each name says which part of the comparison catches it, so a
// comparison that loses that part loses its case here. Code points are
// written as escapes, so the source shows what each one is.
var reservedSpellings = map[string]string{
	"the prefix itself":                      "authn:okta:alice",
	"the prefix and nothing after it":        "authn:",
	"capitals":                               "AUTHN:okta:alice",
	"a capital first letter":                 "Authn:x",
	"mixed case":                             "aUtHn:x",
	"a Cyrillic a":                           "\u0430uthn:x",
	"a Cyrillic capital A":                   "\u0410UTHN:x",
	"a Greek alpha":                          "\u03b1uthn:x",
	"an Armenian u":                          "a\u057dthn:x",
	"a Greek upsilon":                        "a\u03c5thn:x",
	"a Cyrillic te":                          "au\u0442hn:x",
	"a Greek capital tau":                    "AU\u03a4HN:x",
	"a Cyrillic shha":                        "aut\u04bbn:x",
	"a Greek capital eta for the H":          "AUT\u0397N:x",
	"the Planck constant":                    "aut\u210en:x",
	"an Armenian vo":                         "auth\u0578:x",
	"a Greek capital nu":                     "AUTH\u039d:x",
	"a Latin a with a grave":                 "\u00e0uthn:x",
	"a Latin small capital u":                "a\u1d1cthn:x",
	"fullwidth letters and colon":            "\uff41\uff55\uff54\uff48\uff4e\uff1ax",
	"mathematical bold letters":              "\U0001d41a\U0001d42e\U0001d42d\U0001d421\U0001d427:x",
	"mathematical bold capitals":             "\U0001d400\U0001d414\U0001d413\U0001d407\U0001d40d:x",
	"circled letters":                        "\u24d0\u24e4\u24e3\u24d7\u24dd:x",
	"regional indicators":                    "\U0001f1e6\U0001f1fa\U0001f1f9\U0001f1ed\U0001f1f3:x",
	"a combining acute after the a":          "a\u0301uthn:x",
	"a zero-width space inside":              "au\u200bthn:x",
	"a variation selector inside":            "auth\ufe0fn:x",
	"a fullwidth colon":                      "authn\uff1ax",
	"the ratio sign":                         "authn\u2236x",
	"a modifier letter colon":                "authn\ua789x",
	"a modifier letter triangular colon":     "authn\u02d0x",
	"the Armenian full stop":                 "authn\u0589x",
	"a small colon":                          "authn\ufe55x",
	"a Hebrew sof pasuq":                     "authn\u05c3x",
	"the two dot punctuation":                "authn\u205ax",
	"a presentation form for vertical colon": "authn\ufe13x",

	"Lisu capitals":                         "\ua4ee\ua4f4\ua4d4\ua4e7\ua4e0:okta:alice",
	"one Lisu letter among ASCII":           "AUT\ua4e7N:x",
	"Coptic and Armenian capitals":          "\u2c80\u054d\u2ca6\u2c8e\u2c9a:okta:alice",
	"a Coptic small alfa":                   "\u2c81uthn:x",
	"Cherokee small letters":                "\uab7a\u144c\uab72\uab8b\u0578:x",
	"a mathematical Greek small alpha":      "\U0001d6c2uthn:x",
	"a mathematical italic small alpha":     "\U0001d6fcuthn:x",
	"a mathematical Greek capital alpha":    "\U0001d6a8UTHN:x",
	"a mathematical Greek small tau":        "au\U0001d6d5hn:x",
	"a mathematical Greek small upsilon":    "a\U0001d6d6thn:x",
	"a mathematical Greek small nu":         "auth\U0001d6ce:x",
	"the Planck constant over two pi":       "aut\u210fn:x",
	"a Canadian syllabics ne":               "auth\u144e:x",
	"the intersection sign":                 "a\u2229thn:x",
	"a Cyrillic te with descender":          "au\u04adhn:x",
	"a Devanagari visarga":                  "authn\u0903okta:alice",
	"a Gujarati visarga":                    "authn\u0a83okta:alice",
	"a Bengali visarga":                     "authn\u0983x",
	"a Gurmukhi visarga":                    "authn\u0a03x",
	"a Telugu visarga":                      "authn\u0c03x",
	"a Sinhala visarga":                     "authn\u0d83x",
	"a Myanmar visarga":                     "authn\u1038x",
	"a Javanese wignyan":                    "authn\ua983x",
	"the Tibetan rnam bcad":                 "authn\u0f7fokta:alice",
	"a Tibetan gter tsheg":                  "authn\u0f14x",
	"a Syriac supralinear colon":            "authn\u0706x",
	"a Syriac colon skewed left":            "authn\u0708x",
	"the Ethiopic colon":                    "authn\u1365x",
	"the Ethiopic preface colon":            "authn\u1366x",
	"the Bamum colon":                       "authn\ua6f4x",
	"a leading braille blank":               "\u2800authn:okta:alice",
	"a braille blank before the colon":      "authn\u2800:x",
	"a leading Hangul filler":               "\u3164authn:x",
	"a leading Hangul choseong filler":      "\u115fauthn:x",
	"a leading Hangul jungseong filler":     "\u1160authn:x",
	"a leading halfwidth Hangul filler":     "\uffa0authn:x",
	"a Hangul filler inside":                "au\u3164thn:x",
	"a leading zero-width joiner":           "\u200dauthn:x",
	"a leading zero-width non-joiner":       "\u200cauthn:x",
	"a zero-width joiner inside":            "au\u200dthn:x",
	"a zero-width non-joiner at the colon":  "authn\u200c:x",
	"a leading space":                       " authn:x",
	"a no-break space inside":               "auth\u00a0n:x",
	"an ideographic space before the colon": "authn\u3000:x",
	"a Hebrew letter before the prefix":     "\u05d0authn:x",
	"an Arabic letter before the prefix":    "\u0645authn:x",
	"the prefix inside a Hebrew id":         "x\u05d0authn:y",
}

// costOfTheRule are names the rule refuses although they spell nothing like
// the prefix: five characters outside ASCII and then a colon or a sixth such
// character read as "authn:" to a rule that cannot tell what they look like.
var costOfTheRule = map[string]string{
	"five Cyrillic letters and a colon": "\u0411\u043e\u0440\u0438\u0441:x",
	"six Cyrillic letters":              "\u0412\u043b\u0430\u0434\u0438\u043c\u0438\u0440:x",
	"six Hebrew letters":                "\u05d0\u05d1\u05d2\u05d3\u05d4\u05d5",
	"six Hebrew letters after ASCII":    "dana-\u05e9\u05dc\u05d5\u05dd\u05e2\u05dc",
}

// TestAClaimedApproverIDMayNotSpellTheReservedPrefix: in every case, letter
// by letter lookalike and colon lookalike, a claim that reads as the prefix
// is refused with the reserved sentinel.
func TestAClaimedApproverIDMayNotSpellTheReservedPrefix(t *testing.T) {
	for name, id := range reservedSpellings {
		if err := approval.CheckClaimedApproverID(id); !errors.Is(err, approval.ErrReservedApproverID) {
			t.Errorf("%s (%+q): %v, want %v", name, id, err, approval.ErrReservedApproverID)
		}
	}
}

// TestTheRuleRefusesNamesThatOnlyFillThePrefixPositions names what the rule
// costs, so a change that narrows it does so on purpose.
func TestTheRuleRefusesNamesThatOnlyFillThePrefixPositions(t *testing.T) {
	for name, id := range costOfTheRule {
		if err := approval.CheckClaimedApproverID(id); !errors.Is(err, approval.ErrReservedApproverID) {
			t.Errorf("%s (%+q): %v, want %v", name, id, err, approval.ErrReservedApproverID)
		}
	}
}

// TestEveryLookalikeOfAnEarlierTableIsRefused: each letter and colon a
// lookalike table once listed is still refused at its own position.
func TestEveryLookalikeOfAnEarlierTableIsRefused(t *testing.T) {
	at := map[int][]rune{
		0: {0x0430, 0x03b1, 0x03ac, 0x237a, 0x13aa},
		1: {0x03c5, 0x03cd, 0x057d, 0x054d, 0x03bc, 0x222a, 0x22c3, 0x144c},
		2: {0x0442, 0x03c4, 0x13a2},
		3: {0x04bb, 0x0570, 0x043d, 0x045b, 0x03b7, 0x13c2, 0xab92, 0x13bb, 0xab8b, 0x210b, 0x210c, 0x210d, 0x210e},
		4: {0x0578, 0x057c, 0x03b7, 0x03ae, 0x043f, 0x03bd, 0x2115},
		5: {
			0xff1a, 0xfe55, 0xfe13, 0xfe30, 0x2236, 0xa789, 0x02d0, 0x02f8, 0x05c3, 0x0703, 0x0704,
			0x0589, 0xa4fd, 0x1361, 0x16ec, 0x205a, 0x2982, 0x1804, 0x0903, 0x0a83,
		},
	}
	for pos, runes := range at {
		for _, r := range runes {
			id := []rune("authn:x")
			id[pos] = r
			if err := approval.CheckClaimedApproverID(string(id)); !errors.Is(err, approval.ErrReservedApproverID) {
				t.Errorf("U+%04X at position %d (%+q): %v, want it refused", r, pos, string(id), err)
			}
		}
	}
}

// TestAClaimedApproverIDThatOnlyResemblesThePrefixIsAccepted is what keeps
// the refusal above from being a refusal of everything: each id differs from
// the prefix in one place a reader sees.
func TestAClaimedApproverIDThatOnlyResemblesThePrefixIsAccepted(t *testing.T) {
	for _, id := range []string{
		"duty-officer", "authn", "auth:x", "authnx:y", "author:x", "xauthn:y", "authn-x",
		"authn x", "authn;x", "authn.x", "a:b", "zo\u00eb", "Jos\u00e9", "alice@example.com",
		"authenticator", "\u65e5\u672c", "\u0141ukasz:ops", "autm:x", "aut\u0127", "",
		"\u05d0alice", "\u05e9\u05dc\u05d5\u05dd", "alice-\u05e9\u05dc\u05d5\u05dd", "x-\u05d0uthn;y",
	} {
		if err := approval.CheckClaimedApproverID(id); err != nil {
			t.Errorf("%+q: %v, want it accepted", id, err)
		}
	}
}

// visibleOutsideASCII draws a code point outside ASCII that a reader sees as
// a character of its own.
func visibleOutsideASCII() *rapid.Generator[rune] {
	return rapid.RuneFrom(nil, unicode.L, unicode.Mc, unicode.N, unicode.P, unicode.S).Filter(func(r rune) bool {
		return r > unicode.MaxASCII && r != 0x2800 && !unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r)
	})
}

// unseen are code points a reader passes over, one of each class the rule
// skips.
var unseen = []string{
	"", "\u0301", "\u20dd", "\u200b", "\u200c", "\u200d", "\u2060", "\ufe0f", "\U000e0100",
	"\u034f", " ", "\u00a0", "\u3000", "\u2028", "\u2800", "\u3164", "\u115f", "\uffa0",
}

// TestEveryReadingOfThePrefixIsRefused: "authn:" with any of its six
// positions in either case or replaced by a character outside ASCII, and
// with anything a reader passes over before any position, is refused
// whatever follows.
func TestEveryReadingOfThePrefixIsRefused(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var b strings.Builder
		for i, want := range "authn:" {
			b.WriteString(rapid.SampledFrom(unseen).Draw(t, "unseen"))
			switch rapid.IntRange(0, 2).Draw(t, "form") {
			case 0:
				b.WriteRune(want)
			case 1:
				b.WriteRune(unicode.ToUpper(want))
			default:
				b.WriteRune(visibleOutsideASCII().Draw(t, "lookalike"+strconv.Itoa(i)))
			}
		}
		b.WriteString(rapid.String().Draw(t, "rest"))
		id := b.String()
		if err := approval.CheckClaimedApproverID(id); !errors.Is(err, approval.ErrReservedApproverID) {
			t.Fatalf("%+q: %v, want it refused", id, err)
		}
	})
}

// TestTheConstructorBuildsTheReservedSpelling: the one way to the prefix
// writes it in ASCII, joins the issuer and the subject with one colon, and
// holds the whole to the approver id's bound.
func TestTheConstructorBuildsTheReservedSpelling(t *testing.T) {
	got, err := approval.AuthenticatedApproverID("okta", "alice@example.org")
	if err != nil || got != "authn:okta:alice@example.org" {
		t.Fatalf("AuthenticatedApproverID = %q, %v; want %q", got, err, "authn:okta:alice@example.org")
	}
	if got, err := approval.AuthenticatedApproverID("idp", "user:42"); err != nil || got != "authn:idp:user:42" {
		t.Errorf("a subject with a colon = %q, %v; want %q", got, err, "authn:idp:user:42")
	}
	if got, err := approval.AuthenticatedApproverID("my idp.example-1~", "\u0411\u043e\u0440\u0438\u0441"); err != nil || got != "authn:my idp.example-1~:\u0411\u043e\u0440\u0438\u0441" {
		t.Errorf("printable ASCII in the issuer, Cyrillic in the subject = %q, %v; want it built", got, err)
	}
	// "authn:" is 6 bytes, the issuer 1 and its colon 1: a 120-byte subject
	// makes 128.
	if got, err := approval.AuthenticatedApproverID("i", strings.Repeat("s", 120)); err != nil || len(got) != 128 {
		t.Errorf("an id of exactly 128 bytes = %d bytes, %v; want it built", len(got), err)
	}
	for name, c := range map[string]struct{ issuer, subject string }{
		"no issuer":                        {"", "alice"},
		"no subject":                       {"okta", ""},
		"an issuer with a colon":           {"ok:ta", "alice"},
		"an issuer with a fullwidth colon": {"okta\uff1ax", "alice"},
		"an issuer with a Tibetan colon":   {"ok\u0f7fta", "alice"},
		"an issuer with an Ethiopic colon": {"ok\u1365ta", "alice"},
		"an issuer with a braille blank":   {"ok\u2800ta", "alice"},
		"an issuer with a Latin letter":    {"\u00f6kta", "alice"},
		"an issuer with white space":       {" okta", "alice"},
		"a subject with a control":         {"okta", "ali\nce"},
		"a subject with a format":          {"okta", "ali\u202ece"},
		"an id of 129 bytes":               {"i", strings.Repeat("s", 121)},
	} {
		got, err := approval.AuthenticatedApproverID(c.issuer, c.subject)
		if !errors.Is(err, approval.ErrAuthenticatedApprover) || got != "" {
			t.Errorf("%s: %q, %v; want nothing and %v", name, got, err, approval.ErrAuthenticatedApprover)
		}
	}
}

// TestNoClaimCanBeWhatTheConstructorBuilds: whatever issuer and subject the
// constructor accepts, its id is one every claimed path refuses, and it is
// an identifier within the bound the store holds a record to.
func TestNoClaimCanBeWhatTheConstructorBuilds(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		issuer := rapid.String().Draw(t, "issuer")
		subject := rapid.String().Draw(t, "subject")
		id, err := approval.AuthenticatedApproverID(issuer, subject)
		if err != nil {
			return
		}
		if !strings.HasPrefix(id, "authn:") || len(id) > 128 {
			t.Fatalf("built %q", id)
		}
		if err := contract.CheckIdentifier(id); err != nil {
			t.Fatalf("built %q, which is no identifier: %v", id, err)
		}
		if err := approval.CheckClaimedApproverID(id); !errors.Is(err, approval.ErrReservedApproverID) {
			t.Fatalf("built %q, which a claim may spell: %v", id, err)
		}
	})
}

// FuzzCheckClaimedApproverID: no input panics, and every input whose first
// six bytes are the prefix in any ASCII case is refused, whatever follows.
func FuzzCheckClaimedApproverID(f *testing.F) {
	for _, id := range reservedSpellings {
		f.Add(id)
	}
	for _, id := range costOfTheRule {
		f.Add(id)
	}
	for _, id := range []string{"duty-officer", "authn", "", "\xff\xfe", "a\u0301", "zo\u00eb", "\u05d0alice", strings.Repeat("\u200b", 64)} {
		f.Add(id)
	}
	f.Fuzz(func(t *testing.T, id string) {
		err := approval.CheckClaimedApproverID(id)
		if err != nil && !errors.Is(err, approval.ErrReservedApproverID) {
			t.Fatalf("%+q: %v, a refusal of another kind", id, err)
		}
		if len(id) >= 6 && strings.EqualFold(id[:6], "authn:") && err == nil {
			t.Fatalf("%+q: accepted", id)
		}
	})
}
