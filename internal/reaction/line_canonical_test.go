package reaction_test

import (
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// escaped spells s's character at i as a JSON escape of four hex digits.
func escaped(s string, i int, hex string) string {
	return s[:i] + "\\" + "u" + hex + s[i+1:]
}

// TestParseLineTakesOnlyItsCanonicalSpelling: a line that reads as the
// golden one but is spelled otherwise is refused, each way of spelling it.
func TestParseLineTakesOnlyItsCanonicalSpelling(t *testing.T) {
	once := func(s, old, repl string) string {
		if !strings.Contains(s, old) {
			t.Fatalf("%q is not in %s", old, s)
		}
		return strings.Replace(s, old, repl, 1)
	}
	for _, c := range []struct{ name, line string }{
		{"a carriage return before the newline", headerGolden + "\r"},
		{"a leading space", " " + headerGolden},
		{"a trailing space", headerGolden + " "},
		{"a tab between members", once(headerGolden, `,"list_id"`, ",\t\"list_id\"")},
		{"a space after a colon", once(coveredGolden, `"kind":"covered"`, `"kind": "covered"`)},
		{"an escaped member name", escaped(headerGolden, strings.Index(headerGolden, `kind"`)+1, "0069")},
		{"an escaped kind", escaped(coveredGolden, strings.Index(coveredGolden, `covered"`), "0063")},
		{"an escaped finding id", escaped(stopGolden, strings.Index(stopGolden, `-0001"`), "002d")},
		{"an escaped run id", escaped(coveredGolden, strings.Index(coveredGolden, `-a"`), "002d")},
		{"an escaped time", escaped(coveredGolden, strings.Index(coveredGolden, `T12:05`), "0054")},
		{"an escaped route digest", escaped(headerGolden, strings.Index(headerGolden, `:`+bareDigest), "003a")},
		{"members out of order", once(coveredGolden, `{"created_at":"2026-10-06T12:05:00Z","finding_id":"fnd-0002","kind":"covered",`,
			`{"kind":"covered","created_at":"2026-10-06T12:05:00Z","finding_id":"fnd-0002",`)},
	} {
		if c.line == headerGolden || c.line == stopGolden || c.line == coveredGolden {
			t.Fatalf("%s: the line is the golden one", c.name)
		}
		_, err := reaction.ParseLine([]byte(c.line))
		expectOnly(t, c.name, err, reaction.ErrLineCanonical, lineRefusals())
	}
}

// TestJudgeRefusesALineNotSpelledCanonically: a list whose stop is spelled
// otherwise than its writer spells it is unknown, malformed.
func TestJudgeRefusesALineNotSpelledCanonically(t *testing.T) {
	r := listRoute(t)
	b := newList(t, r)
	stop, err := stopOf(t, "fnd-a1", "run-a", clock0, time.Hour).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	b.raw(escaped(string(stop), strings.Index(string(stop), `-a1"`), "002d"))
	s := reaction.Snapshot{}.Next(r, b.bytes(), clock0, poll)
	if s.State() != reaction.Unknown || s.Cause() != reaction.CauseMalformed || !strings.Contains(s.Detail(), "line 2:") {
		t.Fatalf("%s %s %q", s.State(), s.Cause(), s.Detail())
	}
}
