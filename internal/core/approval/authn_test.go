package approval_test

import (
	"errors"
	"strings"
	"testing"

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

// TestAClaimedApproverIDThatOnlyResemblesThePrefixIsAccepted is what keeps
// the refusal above from being a refusal of everything: each id differs from
// the prefix in one place a reader sees.
func TestAClaimedApproverIDThatOnlyResemblesThePrefixIsAccepted(t *testing.T) {
	for _, id := range []string{
		"duty-officer", "authn", "auth:x", "authnx:y", "author:x", "xauthn:y", "authn-x",
		"authn x", "authn;x", "authn.x", "a:b", "zo\u00eb", "\u65e5\u672c",
		"\u0412\u043b\u0430\u0434\u0438\u043c\u0438\u0440:x", "\u0411\u043e\u0440\u0438\u0441:x",
		"autm:x", "aut\u0127", "", " authn:x",
	} {
		if err := approval.CheckClaimedApproverID(id); err != nil {
			t.Errorf("%+q: %v, want it accepted", id, err)
		}
	}
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
	// "authn:" is 6 bytes, the issuer 1 and its colon 1: a 120-byte subject
	// makes 128.
	if got, err := approval.AuthenticatedApproverID("i", strings.Repeat("s", 120)); err != nil || len(got) != 128 {
		t.Errorf("an id of exactly 128 bytes = %d bytes, %v; want it built", len(got), err)
	}
	for name, c := range map[string]struct{ issuer, subject string }{
		"no issuer":                  {"", "alice"},
		"no subject":                 {"okta", ""},
		"an issuer with a colon":     {"ok:ta", "alice"},
		"an issuer with a lookalike": {"okta\uff1ax", "alice"},
		"an issuer with white space": {" okta", "alice"},
		"a subject with a control":   {"okta", "ali\nce"},
		"a subject with a format":    {"okta", "ali\u202ece"},
		"an id of 129 bytes":         {"i", strings.Repeat("s", 121)},
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
	for _, id := range []string{"duty-officer", "authn", "", "\xff\xfe", "a\u0301"} {
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
