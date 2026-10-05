package secretscan

import (
	"encoding/json"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

var escapeRE = regexp.MustCompile(`\\(?:u[0-9a-fA-F]{4}|["\\/bfnrt])`)

// referenceUnescape decodes the escapes a regular expression finds, leftmost
// first, and each contiguous run of \u escapes as UTF-16.
func referenceUnescape(text string) string {
	var b strings.Builder
	var units []uint16
	flush := func() {
		b.WriteString(string(utf16.Decode(units)))
		units = units[:0]
	}
	at := 0
	for _, m := range escapeRE.FindAllStringIndex(text, -1) {
		if m[0] != at {
			flush()
			b.WriteString(text[at:m[0]])
		}
		at = m[1]
		esc := text[m[0]:m[1]]
		if esc[1] != 'u' {
			flush()
			b.WriteString(map[byte]string{'"': `"`, '\\': `\`, '/': "/", 'b': "\b", 'f': "\f", 'n': "\n", 'r': "\r", 't': "\t"}[esc[1]])
			continue
		}
		v := 0
		for _, h := range strings.ToLower(esc[2:]) {
			v = v*16 + strings.IndexRune("0123456789abcdef", h)
		}
		units = append(units, uint16(v))
	}
	flush()
	b.WriteString(text[at:])
	return b.String()
}

var escapeChunks = []string{
	"a", "b", " ", "+", "%", "%2", "%5C", "%5c", "%75", "%61", "%25", "%2F", "%2b", "u", "0", "/", "\\",
	`\\`, `\/`, `\n`, `\"`, `\x`, `\u0061`, `\u002B`, `\u0025`, `\u005C`,
	`\u00e9`, `\uD83D`, `\ude00`, `\udc00`, `\u12`, "é", "%5Cu0061", "%5C%2F", `\%750025`,
}

var decodedChunks = []string{"a", "b", " ", "+", "%", "\\", "/", "\n", "é", "😀", "\uFFFD"}

func chunkText(r *rand.Rand, chunks []string, maxChunks int) string {
	var b strings.Builder
	for range r.IntN(maxChunks + 1) {
		b.WriteString(chunks[r.IntN(len(chunks))])
	}
	return b.String()
}

func TestJSONUnescapeAgreesWithItsReferences(t *testing.T) {
	r := rand.New(rand.NewPCG(11, 5)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
	valid := 0
	for range 20000 {
		text := chunkText(r, escapeChunks, 10)
		got := jsonUnescape(text)
		if want := referenceUnescape(text); got != want {
			t.Fatalf("text %q: got %q, reference %q", text, got, want)
		}
		var std string
		if json.Unmarshal([]byte(`"`+text+`"`), &std) != nil {
			continue
		}
		valid++
		if got != std {
			t.Fatalf("text %q: got %q, encoding/json %q", text, got, std)
		}
	}
	if valid < 2000 {
		t.Fatalf("only %d texts were valid JSON string bodies", valid)
	}
}

func randomEscapeEntries(r *rand.Rand) []entry {
	entries := make([]entry, 1+r.IntN(4))
	for i := range entries {
		entries[i].key = string(rune('A' + i))
		for j := range 1 + r.IntN(3) {
			entries[i].patterns = append(entries[i].patterns, pattern{
				text:     chunkText(r, decodedChunks, 3) + "a",
				spelling: string(rune('p' + j)),
			})
		}
	}
	return entries
}

// TestMatcherAgreesWithEveryDecoding: each pass of match against the
// reference decodings, with a minimum of matches through each pass.
func TestMatcherAgreesWithEveryDecoding(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 9)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
	bySuffix := map[string]int{}
	for range 3000 {
		entries := randomEscapeEntries(r)
		s := &Set{m: newMatcher(entries)}
		for range 20 {
			text := chunkText(r, escapeChunks, 8)
			want, wantOK := referenceMatch(entries, text)
			got, gotOK := s.match(text)
			if got != want || gotOK != wantOK {
				t.Fatalf("entries %+v text %q: got %+v %v, want %+v %v", entries, text, got, gotOK, want, wantOK)
			}
			if wantOK {
				bySuffix[strings.TrimPrefix(got.Spelling, got.Spelling[:1])]++
			}
		}
	}
	for _, suffix := range []string{"", afterPercentDecoding, afterJSONUnescaping, afterJSONThenPercent, afterPercentThenJSON} {
		if bySuffix[suffix] < 50 {
			t.Fatalf("only %d matches %q; the property examined too little: %v", bySuffix[suffix], suffix, bySuffix)
		}
	}
}
