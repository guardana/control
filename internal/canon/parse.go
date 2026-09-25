package canon

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// CanonicalizeJSON parses raw as a single JSON value and canonicalizes it.
//
// It reads the token stream rather than unmarshalling, because three of the
// things a digest cannot survive are invisible once a document has become a Go
// value: a duplicate key (two documents, one canonical form), a number that
// carried a fraction or an exponent, and nesting the encoder would refuse
// anyway after the decoder had walked it.
//
// Input that is not valid UTF-8, and input carrying an unpaired surrogate
// escape, are refused rather than decoded: encoding/json turns both into U+FFFD
// while JavaScript and Python keep the code unit, so the document would have two
// canonical forms and the mismatch would read as tampering.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	v, err := parseJSON(raw)
	if err != nil {
		return nil, err
	}
	return Canonicalize(v)
}

// parseJSON is CanonicalizeJSON without the encode: the value tree, with every
// refusal the canonical form makes about a document already applied. The digest
// embeds authorized arguments in a larger value, so it needs the tree rather
// than the bytes.
func parseJSON(raw []byte) (any, error) {
	if !utf8.Valid(raw) {
		return nil, errors.New("canon: input is not valid UTF-8")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	p := &parser{dec: dec}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	// The parse stops at the end of the first value, so without this a document
	// that continues past it would canonicalize to its prefix.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("canon: trailing data after the top-level value")
	}
	if i := unpairedSurrogate(raw); i >= 0 {
		return nil, fmt.Errorf("%w at byte %d: unpaired surrogate escape", ErrUnsupportedValue, i)
	}
	return v, nil
}

// parser builds the value tree from the token stream. Every refusal it reports
// is in document order, which is fixed by the input rather than by Go's map
// iteration, so the same document always names the same member.
type parser struct {
	walker
	dec *json.Decoder
}

// value reads one value. depth is the number of containers enclosing it.
func (p *parser) value(depth int) (any, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return nil, fmt.Errorf("canon: parse: %w", err)
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return p.array(depth)
		}
		if t == '{' {
			return p.object(depth)
		}
		// A closing delimiter cannot open a value; the decoder rejects the
		// document before this is reachable.
		return nil, fmt.Errorf("canon: parse: unexpected %q", t)
	case json.Number:
		return p.integer(t)
	default: // nil, bool, string
		return tok, nil
	}
}

func (p *parser) array(depth int) ([]any, error) {
	level := depth + 1
	if level > maxDepth {
		return nil, p.tooDeep(level)
	}
	items := []any{}
	for p.dec.More() {
		p.push(strconv.Itoa(len(items)))
		item, err := p.value(level)
		p.pop()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, p.closing()
}

func (p *parser) object(depth int) (map[string]any, error) {
	level := depth + 1
	if level > maxDepth {
		return nil, p.tooDeep(level)
	}
	members := map[string]any{}
	// The document position of each member so far, by fold key.
	folded := map[string]int{}
	for p.dec.More() {
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		// A repeated key is the one input that gives two documents the same
		// digest. Consumers disagree on which occurrence wins - protobuf's JSON
		// parser refuses them, some parsers keep the first, encoding/json keeps
		// the last - so an approval could cover a value nobody was shown.
		if _, seen := members[key]; seen {
			return nil, p.unsupported(fmt.Sprintf("duplicate object key at member %d", len(members)))
		}
		// The same is true for a consumer that matches names without regard to
		// case, so keys that fold together are refused the same way, in the
		// same document order.
		fold := FoldKey(key)
		if earlier, seen := folded[fold]; seen {
			return nil, p.unsupported(foldsTogether(len(members), earlier))
		}
		folded[fold] = len(members)
		p.push(key)
		v, err := p.value(level)
		p.pop()
		if err != nil {
			return nil, err
		}
		members[key] = v
	}
	return members, p.closing()
}

func (p *parser) key() (string, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return "", fmt.Errorf("canon: parse: %w", err)
	}
	key, ok := tok.(string)
	if !ok {
		// Unreachable for valid JSON: only a string can name a member.
		return "", fmt.Errorf("canon: parse: object key is %T", tok)
	}
	return key, nil
}

// closing consumes the ] or } that ends a container.
func (p *parser) closing() error {
	if _, err := p.dec.Token(); err != nil {
		return fmt.Errorf("canon: parse: %w", err)
	}
	return nil
}

// integer accepts an integer literal only. 1.0 and 1e3 hold integral values but
// arrive through a float everywhere else, and ADR-0005 refuses floats.
func (p *parser) integer(num json.Number) (int64, error) {
	literal := num.String()
	if strings.ContainsAny(literal, ".eE") {
		return 0, p.unsupported("number is not an integer literal")
	}
	// The syntax is the decoder's; the only failure left is too long for int64.
	i, err := strconv.ParseInt(literal, 10, 64)
	if err != nil || i > maxSafeInteger || i < minSafeInteger {
		return 0, p.unsupported(outsideRange)
	}
	return i, nil
}

// unpairedSurrogate returns the offset of a \uD800-\uDFFF escape in raw that is
// not half of a valid pair, or -1. raw must already have parsed: valid JSON
// carries no backslash outside a string, so stepping over each escape whole is
// what keeps the second half of \\ from being read as the start of one.
func unpairedSurrogate(raw []byte) int {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		high, n, ok := unicodeEscape(raw[i:])
		if !ok {
			i++ // a two-character escape, \\ among them
			continue
		}
		if !utf16.IsSurrogate(high) {
			i += n - 1
			continue
		}
		low, m, ok := unicodeEscape(raw[i+n:])
		if !ok || utf16.DecodeRune(high, low) == utf8.RuneError {
			return i
		}
		i += n + m - 1
	}
	return -1
}

// unicodeEscape decodes a \uXXXX escape at the start of b and returns the bytes
// it spans, so a caller stepping over it never spells the width out.
func unicodeEscape(b []byte) (rune, int, bool) {
	const width = 6
	var pair [2]byte
	if len(b) < width || b[0] != '\\' || b[1] != 'u' {
		return 0, 0, false
	}
	if _, err := hex.Decode(pair[:], b[2:width]); err != nil {
		return 0, 0, false
	}
	return rune(pair[0])<<8 | rune(pair[1]), width, true
}
