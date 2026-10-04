package observe_test

import (
	"bytes"
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/guardana/control/internal/observe"
	"google.golang.org/protobuf/proto"
)

// mustEscape is every rune a line carries as a \u escape, spelled out rather
// than read from the escape's own rule, and the same set the evidence writer's
// tests spell out: DEL and the C1 controls, the line and paragraph
// separators, and the twelve bidirectional controls.
func mustEscape() map[rune]bool {
	set := map[rune]bool{}
	for _, r := range []rune{
		0x2028, 0x2029,
		0x061c, 0x200e, 0x200f,
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e,
		0x2066, 0x2067, 0x2068, 0x2069,
	} {
		set[r] = true
	}
	for r := rune(0x7f); r <= 0x9f; r++ {
		set[r] = true
	}
	return set
}

func TestMarshalLineEscapesEachRuneOfTheSet(t *testing.T) {
	for r := range mustEscape() {
		o := withName("x" + string(r) + "y")
		line, err := observe.MarshalLine(obsRecord(o))
		if err != nil {
			t.Fatalf("%U: %v", r, err)
		}
		if bytes.ContainsRune(line, r) {
			t.Errorf("%U: the line holds it raw: %q", r, line)
		}
		if want := fmt.Sprintf(`"name":"x\u%04xy"`, r); !bytes.Contains(line, []byte(want)) {
			t.Errorf("%U: the line %q does not hold %s", r, line, want)
		}
		back, err := observe.UnmarshalLine(line)
		if err != nil || !proto.Equal(back, obsRecord(o)) {
			t.Errorf("%U: the line reads back as %v, %v", r, back, err)
		}
	}
}

// The neighbours of each range and ordinary text beyond ASCII reach the line
// as written.
func TestMarshalLineLeavesOtherTextRaw(t *testing.T) {
	for _, r := range []rune{
		'~', 0xa0, 0xe9, 0x061b, 0x061d, 0x200d, 0x2010, 0x2027, 0x202f, 0x2065, 0x206a, 0x1f600,
	} {
		line, err := observe.MarshalLine(obsRecord(withName("x" + string(r) + "y")))
		if err != nil {
			t.Fatalf("%U: %v", r, err)
		}
		if want := `"name":"x` + string(r) + `y"`; !bytes.Contains(line, []byte(want)) {
			t.Errorf("%U: the line %q does not hold %s", r, line, want)
		}
	}
}

// Every rune, one at a time: the escape writes exactly the set above as
// escapes and passes every other rune through, so a rule that drifts from the
// evidence writer's set fails here.
func TestTheEscapeSetOverEveryRune(t *testing.T) {
	set := mustEscape()
	var b []byte
	wrong := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		b = utf8.AppendRune(b[:0], r)
		want := b
		if set[r] {
			want = fmt.Appendf(nil, `\u%04x`, r)
		}
		if got := observe.EscapeLine(b); !bytes.Equal(got, want) {
			t.Errorf("%U escapes as %q, want %q", r, got, want)
			if wrong++; wrong == 20 {
				t.Fatal("stopping after 20 runes")
			}
		}
	}
}
