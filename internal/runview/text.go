package runview

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// text is a string an input chose, as the page draws it: at most MaxText
// characters, each that does not print escaped, and whether more were cut.
// The template places it as text content only.
type text struct {
	S   string
	Cut bool
}

// textOf escapes what does not print, so a direction override, a line
// break or a byte that is no UTF-8 cannot change how the rest reads, and
// cuts at MaxText characters, an escape counting as one.
func textOf(s string) text {
	var b strings.Builder
	for n := 0; s != ""; n++ {
		if n == MaxText {
			return text{S: b.String(), Cut: true}
		}
		r, size := utf8.DecodeRuneInString(s)
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[0])
		case r < utf8.RuneSelf && !unicode.IsGraphic(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case !unicode.IsGraphic(r) || r == '\\':
			if r > 0xffff {
				fmt.Fprintf(&b, `\U%08x`, r)
			} else {
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		default:
			b.WriteRune(r)
		}
		s = s[size:]
	}
	return text{S: b.String()}
}
