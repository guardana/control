package otel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
)

// object reads raw as one JSON object whose members are among names, each
// once, and returns them without the ones that are null. Nothing may follow
// the object. Its refusals name no member of the request's, only the length
// of one, because the names come from whoever sent it.
func object(raw []byte, what string, names ...string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, refuse("%s is not a JSON object", what)
	}
	seen := map[string]bool{}
	out := map[string]json.RawMessage{}
	for dec.More() {
		name, value, err := member(dec, what, names)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, refuse("%s holds its member %s twice", what, name)
		}
		seen[name] = true
		if string(value) != "null" {
			out[name] = value
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, refuse("%s does not end", what)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, refuse("%s is followed by more", what)
	}
	return out, nil
}

// member reads one member of an object, whose name has to be among names.
func member(dec *json.Decoder, what string, names []string) (string, json.RawMessage, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", nil, refuse("the members of %s do not parse", what)
	}
	name, _ := tok.(string)
	if !slices.Contains(names, name) {
		return "", nil, refuse("%s holds a member of %d bytes this receiver does not read", what, len(name))
	}
	var value json.RawMessage
	if err := dec.Decode(&value); err != nil {
		return "", nil, refuse("the member %s of %s does not parse", name, what)
	}
	return name, value, nil
}

// each reads raw, when present, as a JSON array and hands each element to
// read.
func each(raw json.RawMessage, what string, read func(json.RawMessage) error) error {
	if raw == nil {
		return nil
	}
	var elements []json.RawMessage
	if json.Unmarshal(raw, &elements) != nil {
		return refuse("%s is not a JSON array", what)
	}
	for _, element := range elements {
		if err := read(element); err != nil {
			return err
		}
	}
	return nil
}

// text reads raw, when present, as a JSON string, and refuses one escaping
// half of a surrogate pair.
func text(raw json.RawMessage, what string) (string, bool, error) {
	if raw == nil {
		return "", false, nil
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false, refuse("%s is not a JSON string", what)
	}
	if !pairedSurrogates(raw) {
		return "", false, refuse("%s escapes half of a surrogate pair", what)
	}
	return s, true, nil
}

// pairedSurrogates reports whether every \u escape of a surrogate in a JSON
// string that parsed is a high one followed at once by a low one.
func pairedSurrogates(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		r := escaped(raw, i)
		switch {
		case r < 0:
		case isLowSurrogate(r):
			return false
		case r >= 0xd800 && r <= 0xdbff:
			// The low half's backslash is at i+5 and its u at i+6.
			if i+5 >= len(raw) || raw[i+5] != '\\' || !isLowSurrogate(escaped(raw, i+6)) {
				return false
			}
			i += 10
		default:
			i += 4
		}
	}
	return true
}

func isLowSurrogate(r int) bool { return r >= 0xdc00 && r <= 0xdfff }

// escaped reads the \u escape whose u is at raw[at], or -1 when there is none.
func escaped(raw []byte, at int) int {
	if at >= len(raw) || raw[at] != 'u' {
		return -1
	}
	return hex4(raw, at+1)
}

// hex4 reads the four hex digits at raw[at:], or -1 when they are not there.
func hex4(raw []byte, at int) int {
	if at+4 > len(raw) {
		return -1
	}
	n, err := strconv.ParseUint(string(raw[at:at+4]), 16, 32)
	if err != nil {
		return -1
	}
	return int(n)
}
