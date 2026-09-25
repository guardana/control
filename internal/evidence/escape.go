package evidence

import (
	"bytes"
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// maxQuotedIDBytes bounds how much of one producer identifier a refusal
// quotes. A refusal names the event it is about, so the identifier is there to
// be recognised rather than reproduced; two identifiers of 200 KB each made a
// ValidateChain refusal of 400 KB before this bound.
const maxQuotedIDBytes = 64

// quoted cuts s to at most limit bytes, quotes it and marks a cut. It is the
// one way a producer's bytes enter a refusal in this package.
//
// strconv.QuoteToASCII escapes every rune outside printable ASCII and every
// byte that is not valid UTF-8, so a cut that lands inside a rune is escaped
// rather than emitted. Escaping only what strconv.IsPrint rejects is not
// enough: a combining mark, U+3164 and U+2800 are printable, so a refusal
// quoting an NFD spelling beside its NFC one, or a value ending in a filler,
// would show an operator the same text twice. The cost is that an identifier
// in another script reads as escapes.
func quoted(s string, limit int) string {
	if len(s) > limit {
		return strconv.QuoteToASCII(s[:limit]) + " (truncated)"
	}
	return strconv.QuoteToASCII(s)
}

// quoteID quotes one producer identifier for a refusal.
func quoteID(id string) string { return quoted(id, maxQuotedIDBytes) }

// cause reduces the codec's message to something safe to write down. protojson
// quotes the token it refused word for word, so its message carries a
// producer's bytes like any identifier does.
func cause(err error) string { return quoted(err.Error(), maxCauseBytes) }

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
