package keytext

import (
	"encoding/base64"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PKCS8Prefix is the DER RFC 8410 puts before the 32-byte seed of every
// Ed25519 key in PKCS#8: a SEQUENCE of version 0, the id-Ed25519 algorithm
// identifier and an OCTET STRING wrapping an OCTET STRING.
const PKCS8Prefix = "\x30\x2e\x02\x01\x00\x30\x05\x06\x03\x2b\x65\x70\x04\x22\x04\x20"

// bodyStart is how the one body line of every key file keygen writes begins,
// whatever the key: the characters of the prefix's standard base64 that no
// seed bit reaches, six bits to a character.
var bodyStart = base64.StdEncoding.EncodeToString([]byte(PKCS8Prefix))[:len(PKCS8Prefix)*8/6]

// The two PEM markers, and the dashes that close either.
const (
	pemBegin = "-----BEGIN"
	pemEnd   = "-----END"
	pemClose = "-----"
)

// Withheld stands in for the key text Printable takes out of a message.
const Withheld = "[key text withheld]"

// Holds reports whether s holds a PEM marker or the start of a key file's
// body line. It matches only the contiguous start of a body line: a spaced or
// partial one passes.
func Holds(s string) bool {
	return strings.Contains(s, pemBegin) || strings.Contains(s, pemEnd) || strings.Contains(s, bodyStart)
}

// Printable is s as a program may print it when s comes from a setting, a
// file, a record or the environment. Key text is replaced with Withheld and
// the rest is kept: a PEM block from its opening marker through its closing
// one, or to the end of s when it has none, a closing marker alone, and a run
// of base64, in either alphabet, holding the start of a key file's body line.
// What is left comes back as a quoted Go string when it holds a control
// character, a line or paragraph separator, a format character (the
// bidirectional controls among them) or bytes that are not UTF-8, so it stays
// on one line and a terminal obeys none of it; otherwise it comes back as it
// is.
func Printable(s string) string {
	s = withhold(s)
	if !utf8.ValidString(s) || strings.IndexFunc(s, breaksALine) >= 0 {
		return strconv.Quote(s)
	}
	return s
}

func breaksALine(r rune) bool {
	return unicode.IsControl(r) || unicode.In(r, unicode.Zl, unicode.Zp, unicode.Cf)
}

func withhold(s string) string {
	var b strings.Builder
	for {
		start, end, found := next(s)
		if !found {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:start])
		b.WriteString(Withheld)
		s = s[end:]
	}
}

// next finds the first span of key text in s.
func next(s string) (start, end int, found bool) {
	start = len(s)
	if i := strings.Index(s, pemBegin); i >= 0 {
		start, end = i, len(s)
		if j := strings.Index(s[i:], pemEnd); j >= 0 {
			end = closeOf(s, i+j)
		}
	}
	if i := strings.Index(s, pemEnd); i >= 0 && i < start {
		start, end = i, closeOf(s, i)
	}
	if i := strings.Index(s, bodyStart); i >= 0 && i < start {
		start, end = i, i+len(bodyStart)
		for start > 0 && isBase64(s[start-1]) {
			start--
		}
		for end < len(s) && isBase64(s[end]) {
			end++
		}
	}
	return start, end, start < len(s)
}

// closeOf is where the closing marker at i ends: past the dashes that close
// it, or at the end of s when none do.
func closeOf(s string, i int) int {
	after := i + len(pemEnd)
	if j := strings.Index(s[after:], pemClose); j >= 0 {
		return after + j + len(pemClose)
	}
	return len(s)
}

// isBase64 takes both alphabets, standard and URL-safe: a URL-safe body line
// begins with the same characters, and its seed may hold - and _.
func isBase64(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
		c == '+' || c == '/' || c == '-' || c == '_' || c == '='
}
