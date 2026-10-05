package secretscan

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const (
	afterPercentDecoding = " after percent-decoding"
	afterJSONUnescaping  = " after JSON-unescaping"
	afterJSONThenPercent = " after JSON-unescaping and percent-decoding"
	afterPercentThenJSON = " after percent-decoding and JSON-unescaping"
	asAnInteger          = " as an integer"
)

// entry is one scanned secret: its key and every spelling of its value.
type entry struct {
	key      string
	patterns []pattern
}

// scanDocument walks one JSON document token by token. json.Valid runs
// first: the walk stops at the first match, so it alone would read a match
// ahead of trailing garbage as a document.
func (s *Set) scanDocument(doc []byte) Verdict {
	if !json.Valid(doc) {
		return Verdict{}
	}
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Verdict{}
		}
		if v, ok := s.matchToken(tok); ok {
			return v
		}
	}
	return Verdict{State: Clean}
}

// matchToken searches a token's text and, for a number, the integer it
// denotes.
func (s *Set) matchToken(tok json.Token) (Verdict, bool) {
	if v, ok := s.match(tokenText(tok)); ok {
		return v, true
	}
	if n, ok := tok.(json.Number); ok {
		if integer, ok := integerForm(n.String()); ok {
			return s.find(integer, asWritten, asAnInteger)
		}
	}
	return Verdict{}, false
}

// tokenText is the text a key, string or number token carries.
func tokenText(tok json.Token) string {
	switch t := tok.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

// match searches one text as written, JSON-unescaped, and each of those
// percent-decoded, then percent-decoded and JSON-unescaped. A pass whose
// decoding would leave the text unchanged is skipped: an earlier pass has
// searched the same bytes.
func (s *Set) match(text string) (Verdict, bool) {
	if v, ok := s.matchPercent(text, "", afterPercentDecoding); ok {
		return v, true
	}
	if j := jsonUnescape(text); j != text {
		if v, ok := s.matchPercent(j, afterJSONUnescaping, afterJSONThenPercent); ok {
			return v, true
		}
	}
	return s.matchPercentThenJSON(text)
}

// matchPercent searches a text as written and, when it holds an escape,
// again after percent-decoding, without and then with '+' read as a space.
// A '+' is never part of an escape, so the second decoding differs from the
// first exactly when the text holds one.
func (s *Set) matchPercent(text, written, decoded string) (Verdict, bool) {
	if v, ok := s.find(text, asWritten, written); ok {
		return v, true
	}
	if hasEscape(text) {
		if v, ok := s.find(text, percentEscapes, decoded); ok {
			return v, true
		}
	}
	if strings.IndexByte(text, '+') >= 0 {
		return s.find(text, percentEscapesAndPlus, decoded)
	}
	return Verdict{}, false
}

// matchPercentThenJSON differs from JSON-unescaping first only when the
// percent-decoded text holds a backslash, written or as %5C.
func (s *Set) matchPercentThenJSON(text string) (Verdict, bool) {
	if strings.IndexByte(text, '\\') < 0 && !strings.Contains(text, "%5c") && !strings.Contains(text, "%5C") {
		return Verdict{}, false
	}
	if hasEscape(text) {
		if v, ok := s.findUnescaped(percentDecode(text, percentEscapes)); ok {
			return v, true
		}
	}
	if strings.IndexByte(text, '+') >= 0 {
		return s.findUnescaped(percentDecode(text, percentEscapesAndPlus))
	}
	return Verdict{}, false
}

func (s *Set) findUnescaped(decoded string) (Verdict, bool) {
	if j := jsonUnescape(decoded); j != decoded {
		return s.find(j, asWritten, afterPercentThenJSON)
	}
	return Verdict{}, false
}

func (s *Set) find(text string, d decoding, suffix string) (Verdict, bool) {
	id := s.m.first(text, d)
	if id == noMatch {
		return Verdict{}, false
	}
	r := s.m.refs[id]
	return Verdict{State: Found, Key: r.key, Spelling: r.spelling + suffix}, true
}
