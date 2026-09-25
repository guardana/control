// Package canon serializes a value to the canonical JSON form of RFC 8785,
// restricted to what a second implementation in another language can reproduce
// byte for byte. It is the bottom half of the action digest (ADR-0005), which
// is why input it cannot reproduce is refused rather than approximated.
//
// Object keys sort as arrays of UTF-16 code units (RFC 8785 section 3.2.3),
// which is not UTF-8 byte order. A key holding U+1F600 encodes to the surrogate
// pair D83D DE00 and sorts below a key holding U+E000, while its UTF-8 bytes
// sort above; sort.Strings therefore produces a different digest and passes
// every test written only over the basic multilingual plane.
//
// This file writes the canonical form of a value, parse.go reads a document
// into one, and pointer.go names the value a refusal is about.
package canon

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Reported for input this package refuses, wrapped with the RFC 6901 JSON
// pointer of the offending value so nobody has to bisect a document by hand.
var (
	ErrUnsupportedValue = errors.New("canon: unsupported value")
	ErrTooDeep          = errors.New("canon: nesting too deep")
)

const (
	// maxDepth counts containers, not values: a scalar at the top level sits at
	// depth 0, [] at depth 1, [[]] at depth 2.
	maxDepth = 32

	// The JSON-safe integer range: beyond 2^53-1 no IEEE-754 double is faithful,
	// so a reader going through a JavaScript JSON parser would see another value
	// and compute another digest (ADR-0005).
	maxSafeInteger = 1<<53 - 1
	minSafeInteger = -maxSafeInteger

	// Lowercase, because RFC 8785 section 3.2.2.2 fixes the case of the four hex
	// digits in a \u escape and a digest compares bytes.
	hexDigits = "0123456789abcdef"

	// A refusal names where the value is and what shape it has, never what it
	// holds: the message travels into logs and evidence, and a rejected number
	// literal has no length limit (invariant 9).
	outsideRange = "integer is outside the JSON-safe range +/-(2^53-1)"
)

// Canonicalize returns the RFC 8785 canonical JSON encoding of v.
//
// Supported: nil, bool, string, int, int64, uint64, []any and map[string]any.
// Every other type is ErrUnsupportedValue, including every float type, every
// other integer width and json.Number; so is a string or an object key that is
// not valid UTF-8. Nesting deeper than 32 containers is ErrTooDeep.
func Canonicalize(v any) ([]byte, error) {
	e := &encoder{}
	if err := e.value(v, 0); err != nil {
		return nil, err
	}
	return e.buf.Bytes(), nil
}

type encoder struct {
	walker
	buf bytes.Buffer
}

// value writes v. depth is the number of containers enclosing it.
func (e *encoder) value(v any, depth int) error {
	switch t := v.(type) {
	case nil:
		e.buf.WriteString("null")
	case bool:
		e.buf.WriteString(strconv.FormatBool(t))
	case string:
		if i := invalidUTF8At(t); i >= 0 {
			return e.unsupported(fmt.Sprintf("string is not valid UTF-8 at byte %d of %d", i, len(t)))
		}
		e.writeString(t)
	case int:
		return e.writeSigned(int64(t))
	case int64:
		return e.writeSigned(t)
	case uint64:
		return e.writeUnsigned(t)
	case []any:
		return e.array(t, depth)
	case map[string]any:
		return e.object(t, depth)
	case canonical:
		e.buf.Write(t)
	default:
		return e.unsupported(fmt.Sprintf("type %T", v))
	}
	return nil
}

func (e *encoder) array(a []any, depth int) error {
	level := depth + 1
	if level > maxDepth {
		return e.tooDeep(level)
	}
	e.buf.WriteByte('[')
	for i, item := range a {
		if i > 0 {
			e.buf.WriteByte(',')
		}
		e.push(strconv.Itoa(i))
		err := e.value(item, level)
		e.pop()
		if err != nil {
			return err
		}
	}
	e.buf.WriteByte(']')
	return nil
}

func (e *encoder) object(m map[string]any, depth int) error {
	level := depth + 1
	if level > maxDepth {
		return e.tooDeep(level)
	}
	// Counted before sorting, and counted rather than named: two keys that are
	// not valid UTF-8 can encode to the same UTF-16 code units, so which one a
	// refusal blamed would be left to Go's map iteration.
	bad := 0
	for key := range m {
		if !utf8.ValidString(key) {
			bad++
		}
	}
	if bad > 0 {
		return e.unsupported(fmt.Sprintf("object key is not valid UTF-8 (%d of %d keys)", bad, len(m)))
	}

	// Here and not only in the parser: the attributes and labels of an envelope
	// reach the canonical form as Go maps and never pass the parser.
	keys := sortedKeys(m)
	if later, earlier, found := foldedMember(keys); found {
		return e.unsupported(foldsTogether(later, earlier))
	}

	e.buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			e.buf.WriteByte(',')
		}
		e.writeString(key)
		e.buf.WriteByte(':')
		e.push(key)
		err := e.value(m[key], level)
		e.pop()
		if err != nil {
			return err
		}
	}
	e.buf.WriteByte('}')
	return nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sortUTF16(keys)
	return keys
}

// sortUTF16 sorts in RFC 8785 order: as arrays of UTF-16 code units compared as
// unsigned integers, the shorter value first on a common prefix. It is the only
// order this package sorts anything by, object keys and delegation scopes
// alike, because UTF-8 byte order disagrees with it above the basic
// multilingual plane.
//
// The code units are computed once per value rather than inside the comparator,
// which the digest path pays on every call. Equal values are interchangeable,
// so the unstable sort is still deterministic.
func sortUTF16(values []string) {
	type item struct {
		value string
		units []uint16
	}
	items := make([]item, len(values))
	for i, value := range values {
		items[i] = item{value: value, units: utf16.Encode([]rune(value))}
	}
	slices.SortFunc(items, func(a, b item) int { return slices.Compare(a.units, b.units) })
	for i, it := range items {
		values[i] = it.value
	}
}

// invalidUTF8At returns the offset of the first byte that is not part of a
// valid UTF-8 sequence, or -1. Ranging a string yields U+FFFD of width 1 for
// exactly those bytes, which is what separates them from a real U+FFFD.
func invalidUTF8At(s string) int {
	for i, r := range s {
		if r != utf8.RuneError {
			continue
		}
		if _, width := utf8.DecodeRuneInString(s[i:]); width <= 1 {
			return i
		}
	}
	return -1
}

func (e *encoder) writeString(s string) {
	e.buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if esc := escapeFor(s[i]); esc != "" {
			e.buf.WriteString(esc)
			continue
		}
		e.buf.WriteByte(s[i])
	}
	e.buf.WriteByte('"')
}

const (
	// escapes holds two characters for each byte of named, in the same order.
	named   = "\b\t\n\f\r\"\\"
	escapes = `\b\t\n\f\r\"\\`

	// The two must stay in step, or the slice below reads out of range. Neither
	// blank constant compiles if the lengths diverge, because a negative
	// constant does not convert to uint.
	_ = uint(len(escapes) - 2*len(named))
	_ = uint(2*len(named) - len(escapes))
)

// escapeFor returns the RFC 8785 section 3.2.2.2 escape for c, or "" when c is
// written literally: the space, the solidus and every byte of a multi-byte UTF-8
// sequence, all of which are >= 0x20.
func escapeFor(c byte) string {
	if i := strings.IndexByte(named, c); i >= 0 {
		return escapes[2*i : 2*i+2]
	}
	if c < 0x20 {
		return `\u00` + string([]byte{hexDigits[c>>4], hexDigits[c&0x0f]})
	}
	return ""
}

func (e *encoder) writeSigned(n int64) error {
	if n > maxSafeInteger || n < minSafeInteger {
		return e.unsupported(outsideRange)
	}
	e.buf.WriteString(strconv.FormatInt(n, 10))
	return nil
}

// writeUnsigned formats from uint64, so no value wraps on its way to the check.
func (e *encoder) writeUnsigned(n uint64) error {
	if n > maxSafeInteger {
		return e.unsupported(outsideRange)
	}
	e.buf.WriteString(strconv.FormatUint(n, 10))
	return nil
}
