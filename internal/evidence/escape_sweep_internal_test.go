package evidence

import (
	"bytes"
	"fmt"
	"testing"
	"unicode/utf8"
)

// TestTheEscapeSetOverEveryRune: the writer escapes exactly DEL and the C1
// controls, the line and paragraph separators and the twelve bidirectional
// controls, spelled out here rather than read from the rule, and leaves every
// other valid rune as it is.
func TestTheEscapeSetOverEveryRune(t *testing.T) {
	set := map[rune]bool{}
	for _, r := range []rune{0x2028, 0x2029, 0x061c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069} {
		set[r] = true
	}
	for r := rune(0x7f); r <= 0x9f; r++ {
		set[r] = true
	}
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
		var got bytes.Buffer
		escapeLine(&got, b)
		if !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%U escapes as %q, want %q", r, got.Bytes(), want)
			if wrong++; wrong == 20 {
				t.Fatal("stopping after 20 runes")
			}
		}
	}
}
