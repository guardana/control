package evidence

import (
	"bytes"
	"fmt"
	"unicode"
	"unicode/utf8"

	"github.com/guardana/control/internal/trailchain"
)

func quoted(s string, limit int) string { return trailchain.Quoted(s, limit) }

func quoteID(id string) string { return trailchain.Quoted(id, trailchain.MaxQuotedIDBytes) }

// escapeLine appends line to dst with every rune a reader could take for
// something other than text written as a JSON \u escape. JSON requires the C0
// controls, the quote and the backslash to be escaped and protojson escapes
// nothing more, so without this a written line holds, raw:
//
//   - DEL and the C1 controls, U+007F to U+009F: NEL is a line break to a
//     reader that splits on Unicode line breaks, and CSI starts a command on a
//     terminal that honours C1;
//   - U+2028 and U+2029, which JavaScript and Python both split lines on;
//   - the bidirectional controls, which reorder what a reviewer sees.
//
// Outside a string a JSON line is ASCII, so every such rune sits inside a
// string, where the escaped spelling decodes to the same value and the round
// trip stays proto.Equal. The hex is lowercase, as protojson writes its own.
func escapeLine(dst *bytes.Buffer, line []byte) {
	start := 0
	for i := 0; i < len(line); {
		if b := line[i]; b < utf8.RuneSelf && b != 0x7f {
			i++
			continue
		}
		r, size := utf8.DecodeRune(line[i:])
		if needsEscape(r) {
			dst.Write(line[start:i])
			fmt.Fprintf(dst, `\u%04x`, r)
			start = i + size
		}
		i += size
	}
	dst.Write(line[start:])
}

// needsEscape reports the runes escapeLine writes as escapes.
func needsEscape(r rune) bool {
	return (r >= 0x7f && r <= 0x9f) || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Bidi_Control, r)
}
