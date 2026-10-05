package secretscan

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

const (
	spellingRaw    = "raw"
	spellingJSON   = "json-escaped"
	spellingBase64 = "base64"
)

// minBase64Core is the shortest base64 run searched for; a shorter one would
// match unrelated encoded data too often.
const minBase64Core = 8

const hexLower = "0123456789abcdef"

// pattern is one spelling of one secret.
type pattern struct {
	text     string
	spelling string
}

// spellings lists every distinct text the value is searched for, raw first.
func spellings(value string) ([]pattern, error) {
	escaped, err := jsonEscapes(value)
	if err != nil {
		return nil, err
	}
	out := []pattern{{text: value, spelling: spellingRaw}}
	seen := map[string]bool{value: true}
	add := func(text, spelling string) {
		if seen[text] {
			return
		}
		seen[text] = true
		out = append(out, pattern{text: text, spelling: spelling})
	}
	for _, e := range escaped {
		add(e, spellingJSON)
	}
	for _, c := range base64Cores(value) {
		add(c, spellingBase64)
	}
	return out, nil
}

// jsonEscapes is the value as the body of a JSON string, in every
// combination of the escapes an encoder may choose.
func jsonEscapes(value string) ([]string, error) {
	var out []string
	for _, escapeHTML := range []bool{true, false} {
		body, err := jsonBody(value, escapeHTML)
		if err != nil {
			return nil, err
		}
		for _, s := range []string{body, strings.ReplaceAll(body, "/", `\/`)} {
			ascii := asciiOnly(s)
			out = append(out, s, upperHex(s), ascii, upperHex(ascii))
		}
	}
	return out, nil
}

func jsonBody(value string, escapeHTML bool) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(escapeHTML)
	if err := enc.Encode(value); err != nil {
		return "", fmt.Errorf("secretscan: escape a value: %w", err)
	}
	b := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return "", fmt.Errorf("secretscan: escape a value: encoder wrote %d bytes that are not a string", len(b))
	}
	return string(b[1 : len(b)-1]), nil
}

// asciiOnly writes every non-ASCII rune of a JSON string body as a
// lower-case \u escape, a rune above the BMP as its surrogate pair. The body
// comes from encoding/json, so it is valid UTF-8.
func asciiOnly(body string) string {
	var b strings.Builder
	for _, r := range body {
		if r < 0x80 {
			b.WriteRune(r)
			continue
		}
		if r1, r2 := utf16.EncodeRune(r); r1 != 0xFFFD || r2 != 0xFFFD {
			writeU(&b, r1)
			writeU(&b, r2)
			continue
		}
		writeU(&b, r)
	}
	return b.String()
}

func writeU(b *strings.Builder, r rune) {
	b.WriteByte('\\')
	b.WriteByte('u')
	for shift := 12; shift >= 0; shift -= 4 {
		b.WriteByte(hexLower[(r>>shift)&0xF])
	}
}

// upperHex rewrites the hex digits of every \u escape in a JSON string body
// in upper case, stepping over each escape so an escaped backslash followed
// by a literal u is left alone.
func upperHex(body string) string {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		b.WriteByte(c)
		if c != '\\' || i+1 >= len(body) {
			continue
		}
		i++
		b.WriteByte(body[i])
		if body[i] == 'u' && i+4 < len(body) {
			b.WriteString(strings.ToUpper(body[i+1 : i+5]))
			i += 4
		}
	}
	return b.String()
}

// base64Cores is, for both alphabets and each byte alignment in a longer
// encoded run, the part of the value's encoding that no neighbouring byte
// changes: output character i covers bits [6i, 6i+6), and only characters
// whose bits lie wholly inside the value are kept.
func base64Cores(value string) []string {
	var out []string
	n := len(value)
	for align := 0; align < 3; align++ {
		lo := (8*align + 5) / 6
		hi := (8*align + 8*n) / 6
		if hi-lo < minBase64Core {
			continue
		}
		buf := make([]byte, align+n)
		copy(buf[align:], value)
		for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding} {
			out = append(out, enc.EncodeToString(buf)[lo:hi])
		}
	}
	return out
}
