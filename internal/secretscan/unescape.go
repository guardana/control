package secretscan

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// jsonUnescape decodes every well-formed JSON string escape in the text, one
// pass, and leaves a malformed one as written. A surrogate that is not half
// of a pair becomes U+FFFD, as encoding/json reads it.
func jsonUnescape(text string) string {
	if strings.IndexByte(text, '\\') < 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != '\\' || i+1 == len(text) {
			b.WriteByte(c)
			continue
		}
		if d, ok := simpleEscape(text[i+1]); ok {
			b.WriteByte(d)
			i++
			continue
		}
		r, n := unicodeEscape(text[i:])
		if n == 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteRune(r)
		i += n - 1
	}
	return b.String()
}

func simpleEscape(c byte) (byte, bool) {
	switch c {
	case '"', '\\', '/':
		return c, true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// unicodeEscape reads the \u escape at the start of s, with the low half
// that follows a high surrogate, and reports how many bytes it took, or 0.
func unicodeEscape(s string) (rune, int) {
	r, ok := u4(s)
	if !ok {
		return 0, 0
	}
	if !utf16.IsSurrogate(r) {
		return r, 6
	}
	if low, ok := u4(s[6:]); ok {
		if pair := utf16.DecodeRune(r, low); pair != utf8.RuneError {
			return pair, 12
		}
	}
	return utf8.RuneError, 6
}

func u4(s string) (rune, bool) {
	if len(s) < 6 || s[0] != '\\' || s[1] != 'u' {
		return 0, false
	}
	var r rune
	for i := 2; i < 6; i++ {
		if !isHex(s[i]) {
			return 0, false
		}
		r = r<<4 | rune(unhex(s[i]))
	}
	return r, true
}

// percentDecode is the text the matcher reads with the given decoding.
func percentDecode(text string, d decoding) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); {
		c, n := decodeByte(text, i, d)
		b.WriteByte(c)
		i += n
	}
	return b.String()
}
