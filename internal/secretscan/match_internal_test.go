package secretscan

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// containsFind is the per-pattern search the matcher replaces: the first
// entry, then the first of its patterns, contained in the text.
func containsFind(entries []entry, text, suffix string) (Verdict, bool) {
	for _, e := range entries {
		for _, p := range e.patterns {
			if strings.Contains(text, p.text) {
				return Verdict{State: Found, Key: e.key, Spelling: p.spelling + suffix}, true
			}
		}
	}
	return Verdict{}, false
}

func referenceDecode(text string, plusIsSpace bool) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '%' && i+2 < len(text) && strings.IndexByte("0123456789abcdefABCDEF", text[i+1]) >= 0 &&
			strings.IndexByte("0123456789abcdefABCDEF", text[i+2]) >= 0:
			n := 0
			for _, h := range text[i+1 : i+3] {
				n = n*16 + strings.IndexRune("0123456789abcdef", h|0x20)
			}
			b.WriteByte(byte(n))
			i += 2
		case c == '+' && plusIsSpace:
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func referenceMatch(entries []entry, text string) (Verdict, bool) {
	j := referenceUnescape(text)
	for _, view := range []struct{ text, suffix string }{
		{text, ""},
		{referenceDecode(text, false), afterPercentDecoding},
		{referenceDecode(text, true), afterPercentDecoding},
		{j, afterJSONUnescaping},
		{referenceDecode(j, false), afterJSONThenPercent},
		{referenceDecode(j, true), afterJSONThenPercent},
		{referenceUnescape(referenceDecode(text, false)), afterPercentThenJSON},
		{referenceUnescape(referenceDecode(text, true)), afterPercentThenJSON},
	} {
		if v, ok := containsFind(entries, view.text, view.suffix); ok {
			return v, true
		}
	}
	return Verdict{}, false
}

func randomText(r *rand.Rand, alphabet string, maxLen int) string {
	b := make([]byte, r.IntN(maxLen+1))
	for i := range b {
		b[i] = alphabet[r.IntN(len(alphabet))]
	}
	return string(b)
}

func randomEntries(r *rand.Rand) []entry {
	entries := make([]entry, 1+r.IntN(6))
	for i := range entries {
		entries[i].key = string(rune('A' + i))
		for j := range 1 + r.IntN(4) {
			entries[i].patterns = append(entries[i].patterns, pattern{
				text:     randomText(r, "ab %2+", 4) + "a",
				spelling: string(rune('p' + j)),
			})
		}
	}
	return entries
}

// TestMatcherAgreesWithContains: overlapping patterns, patterns that are
// prefixes and suffixes of others, the same text under two entries, and
// one-byte patterns, against texts with escapes and '+'.
func TestMatcherAgreesWithContains(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 42)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
	found := 0
	for range 3000 {
		entries := randomEntries(r)
		s := &Set{m: newMatcher(entries)}
		for range 20 {
			text := randomText(r, "ab %2+0", 24)
			want, wantOK := referenceMatch(entries, text)
			got, gotOK := s.match(text)
			if got != want || gotOK != wantOK {
				t.Fatalf("entries %+v text %q: got %+v %v, want %+v %v", entries, text, got, gotOK, want, wantOK)
			}
			if wantOK {
				found++
			}
		}
	}
	if found < 1000 {
		t.Fatalf("only %d texts matched; the property examined too little", found)
	}
}
