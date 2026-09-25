// Refusal text. A ValidateChain refusal is the text most likely to reach an
// operator's log, and every identifier it quotes came from a producer, so each
// one is cut and escaped before it is quoted: the rule the decoder's own
// refusals already followed.
package evidence_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/pkg/contract"
)

// refusalSite is one ValidateChain refusal that quotes a producer's value.
// damage breaks a trail there with two values the test chooses: mine is the one
// the trail agrees on, and theirs the one an event carries instead. quoted names
// the values the refusal has to quote, which is both except where the refusal
// is about one value repeating.
type refusalSite struct {
	name   string
	names  string // the event the refusal has to name
	damage func(e []*controlv1.Event, mine, theirs string)
	quoted func(mine, theirs string) []string
}

// refusalSites is every site at which ValidateChain quotes a producer's value.
// A site missing here is one whose quoting no test examines, so a refusal
// there could quote far more than the cut with every test green.
func refusalSites() []refusalSite {
	both := func(mine, theirs string) []string { return []string{mine, theirs} }
	one := func(mine, _ string) []string { return []string{mine} }
	// The trail agrees on mine, and event 2 carries theirs.
	scope := func(set func(*controlv1.Event, string)) func(e []*controlv1.Event, mine, theirs string) {
		return func(e []*controlv1.Event, mine, theirs string) {
			for _, event := range e {
				set(event, mine)
			}
			set(e[2], theirs)
		}
	}
	return []refusalSite{
		{"another request", "event 2", scope(func(e *controlv1.Event, v string) { e.RequestId = v }), both},
		{"another project", "event 2", scope(func(e *controlv1.Event, v string) { e.ProjectId = v }), both},
		{"another tenant", "event 2", scope(func(e *controlv1.Event, v string) { e.TenantId = v }), both},
		{"a repeated event id", "events 1 and 2", func(e []*controlv1.Event, mine, _ string) {
			e[1].EventId = mine
			e[2].EventId = mine
			e[2].PrevEventId = mine
		}, one},
		{"a link to somewhere else", "event 2", func(e []*controlv1.Event, mine, theirs string) {
			e[1].EventId = mine
			e[2].PrevEventId = theirs
		}, both},
		{"another execution", "event 3", func(e []*controlv1.Event, mine, theirs string) {
			e[2].ExecutionId = mine
			e[3].ExecutionId = theirs
		}, both},
	}
}

// refuse breaks a trail at site and returns the refusal's text, failing the
// test unless the refusal is a definite one that names its event.
func refuse(t *testing.T, site refusalSite, mine, theirs string) string {
	t.Helper()
	events := trail(t, ranToCompletion()...)
	site.damage(events, mine, theirs)

	err := evidence.ValidateChain(events)
	if !errors.Is(err, evidence.ErrChainBroken) {
		t.Fatalf("ValidateChain: got %.200v, want ErrChainBroken", err)
	}
	message := err.Error()
	if !strings.Contains(message, site.names) {
		t.Errorf("the refusal does not name %s: %.200q", site.names, message)
	}
	return message
}

// Every site, with a value on each side of the comparison where it has two, as
// long as an identifier may be and still reach the site: contract.MaxStringBytes.
// Past it ValidateChain refuses the identifier, without quoting it, before any
// site sees it (TestValidateChainBoundsEveryIdentifierItReads). Before the cut,
// two values of 200 KB made a refusal of 400 KB.
func TestChainRefusalsQuoteBoundedIdentifiers(t *testing.T) {
	long, other := strings.Repeat("x", contract.MaxStringBytes), strings.Repeat("y", contract.MaxStringBytes)
	for _, site := range refusalSites() {
		t.Run(site.name, func(t *testing.T) {
			if message := refuse(t, site, long, other); len(message) > 512 {
				t.Errorf("the refusal is %d bytes long: %.120q...", len(message), message)
			}
		})
	}
}

// The bound at the byte, at every site. An identifier of 64 bytes is quoted
// whole, and one of 65 is cut to 64 and marked. The ceiling above admits two
// cuts of 200 bytes, so only this pair tells a site that cuts at 64 from one
// that cuts later or not at all.
func TestChainRefusalsCutEveryQuotedIdentifierAt64Bytes(t *testing.T) {
	for _, site := range refusalSites() {
		for _, tc := range []struct {
			length int
			whole  bool
		}{
			{64, true},
			{65, false},
		} {
			t.Run(site.name+"/"+strconv.Itoa(tc.length), func(t *testing.T) {
				mine, theirs := strings.Repeat("m", tc.length), strings.Repeat("t", tc.length)
				message := refuse(t, site, mine, theirs)

				for _, id := range site.quoted(mine, theirs) {
					whole := strconv.Quote(id)
					cut := strconv.Quote(id[:64]) + " (truncated)"
					switch {
					case tc.whole && !strings.Contains(message, whole):
						t.Errorf("a %d-byte identifier was not quoted whole: %q", tc.length, message)
					case !tc.whole && strings.Contains(message, whole):
						t.Errorf("a %d-byte identifier was quoted whole: %q", tc.length, message)
					case !tc.whole && !strings.Contains(message, cut):
						t.Errorf("a %d-byte identifier was not cut to 64 bytes and marked: %q", tc.length, message)
					}
				}
				if tc.whole && strings.Contains(message, "(truncated)") {
					t.Errorf("a %d-byte identifier was marked as cut: %q", tc.length, message)
				}
			})
		}
	}
}

// What is quoted is escaped as well as cut: a control byte, a Unicode line
// separator, a bidirectional override and a byte that is not UTF-8 reach the
// refusal as escapes, so the message stays one line of plain text for any
// reader. The runes are built from their numbers, so this file holds none raw.
func TestChainRefusalsEscapeWhatTheyQuote(t *testing.T) {
	hostile := "a" + string(rune(0x1b)) + "[31m" + string(rune(0x2028)) + "b" +
		string(rune(0x202e)) + "c" + string(rune(0x85)) + "d\xff"
	events := trail(t, ranToCompletion()...)
	events[2].PrevEventId = hostile

	err := evidence.ValidateChain(events)
	if !errors.Is(err, evidence.ErrChainBroken) {
		t.Fatalf("ValidateChain: got %v, want ErrChainBroken", err)
	}
	message := err.Error()
	if !utf8.ValidString(message) {
		t.Errorf("the refusal is not valid UTF-8: %q", message)
	}
	for _, r := range message {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 || r == 0x202e {
			t.Errorf("the refusal holds %U raw: %q", r, message)
		}
	}
}

// Two identifiers that look alike never read alike in a refusal. Every rune
// outside printable ASCII in a quoted value reaches the refusal as an escape,
// so an NFC and an NFD spelling of one word, or a value ending in a filler
// nobody can see, is refused with two texts a reader can tell apart.
// Each of these runes is printable, so an escape that stopped at what
// strconv.IsPrint rejects would print both values alike.
//
// The runes are built from their numbers, and the escapes the refusal has to
// hold are assembled from a backslash built the same way, with the hex digits
// written out by hand. The source holds no escape sequence at all, so no step
// between an editor and this file can turn an expectation into the rune it is
// about.
func TestChainRefusalsTellApartIdentifiersThatLookAlike(t *testing.T) {
	bs := string(rune(0x5c))
	for _, tc := range []struct {
		name        string
		trail, last string   // every event's tenant, and the one the last event carries instead
		want        []string // the two quoted values, spelled as the refusal has to spell them
	}{
		{
			"an NFD spelling of an NFC tenant", "caf" + string(rune(0xe9)), "cafe" + string(rune(0x301)),
			[]string{`"caf` + bs + `u00e9"`, `"cafe` + bs + `u0301"`},
		},
		{
			"a trailing Hangul filler", "tenant-1", "tenant-1" + string(rune(0x3164)),
			[]string{`"tenant-1"`, `"tenant-1` + bs + `u3164"`},
		},
		{
			"a trailing braille blank", "tenant-1", "tenant-1" + string(rune(0x2800)),
			[]string{`"tenant-1"`, `"tenant-1` + bs + `u2800"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := trail(t, ranToCompletion()...)
			for _, event := range events {
				event.TenantId = tc.trail
			}
			events[3].TenantId = tc.last

			err := evidence.ValidateChain(events)
			if !errors.Is(err, evidence.ErrChainBroken) {
				t.Fatalf("ValidateChain: got %v, want ErrChainBroken", err)
			}
			message := err.Error()
			for i := range len(message) {
				if c := message[i]; c < 0x20 || c > 0x7e {
					t.Errorf("byte %d of the refusal is %#x, outside printable ASCII: %+q", i, c, message)
					break
				}
			}
			for _, want := range tc.want {
				if !strings.Contains(message, want) {
					t.Errorf("the refusal does not spell %s: %+q", want, message)
				}
			}
		})
	}
}
