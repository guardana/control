// Package strictjson reads one JSON object whose members are known by their
// exact names, each named once, for the small formats whose every member is
// required and whose decoding must not keep the last of two, and checks the
// version such a format names.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Error is a refusal by this package, matched with errors.Is.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The refusals. None quotes the input.
const (
	// ErrNotObject is input that is not one JSON object with nothing after it.
	ErrNotObject Error = "strictjson: not one JSON object"
	// ErrRepeated is a member named twice, in whatever spelling.
	ErrRepeated Error = "strictjson: a member is named twice"
	// ErrUnknown is a member the format does not have.
	ErrUnknown Error = "strictjson: a member the format does not have"
	// ErrMissing is a member the format requires and the object lacks.
	ErrMissing Error = "strictjson: a member the format requires is missing"
)

// Object is an object's members by their decoded names, each value the bytes
// it was written as.
type Object map[string]json.RawMessage

// ReadObject reads raw as one JSON object with nothing but white space after
// it. A member is known by its name as decoded, so a name spelled once
// plainly and once with escapes is named twice. It does not judge the
// encoding of the values: invalid UTF-8 and an unpaired surrogate pass, as
// they pass encoding/json.
func ReadObject(raw []byte) (Object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%w: it does not open an object", ErrNotObject)
	}
	o := Object{}
	for dec.More() {
		tok, err := dec.Token()
		name, isName := tok.(string)
		if err != nil || !isName {
			return nil, fmt.Errorf("%w: a malformed member", ErrNotObject)
		}
		if _, twice := o[name]; twice {
			return nil, ErrRepeated
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: a malformed value", ErrNotObject)
		}
		o[name] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, fmt.Errorf("%w: an unclosed object", ErrNotObject)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: more after the object", ErrNotObject)
	}
	return o, nil
}

// Only refuses, with ErrUnknown, a member whose name is none of names,
// compared exactly.
func (o Object) Only(names ...string) error {
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	for name := range o {
		if !known[name] {
			return ErrUnknown
		}
	}
	return nil
}

// Require refuses, with ErrMissing naming it, the first of names the object
// does not hold.
func (o Object) Require(names ...string) error {
	for _, name := range names {
		if _, ok := o[name]; !ok {
			return fmt.Errorf("%w: %s", ErrMissing, name)
		}
	}
	return nil
}

// String reads raw as a JSON string and nothing else: not null, which a
// decoder reads into a string as "".
func String(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// IsVersion reports whether v is MAJOR.MINOR under major, as the wire
// contracts spell a version: two parts, each decimal digits within 32 bits
// with no sign, the first spelled exactly as major.
func IsVersion(v, major string) bool {
	m, minor, ok := strings.Cut(v, ".")
	return ok && m == major && isDecimal(m) && isDecimal(minor)
}

// isDecimal is ParseUint at base 10, which takes no sign, no underscore and no
// empty string.
func isDecimal(s string) bool {
	_, err := strconv.ParseUint(s, 10, 32)
	return err == nil
}
