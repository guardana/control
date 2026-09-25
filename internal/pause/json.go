package pause

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

// members reads raw as one JSON object and returns its members by their
// exact names, refusing a member named twice, which encoding/json would read
// as the last one. With whole set, nothing may follow the object.
func members(raw []byte, whole bool) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := tok.(string)
		if _, seen := out[name]; seen {
			return nil, fmt.Errorf("the member %q is given twice", name)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		out[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if whole {
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return nil, errors.New("text after the document")
		}
	}
	return out, nil
}

// only refuses a member whose name is not one of names, spelled exactly.
func only(m map[string]json.RawMessage, names ...string) error {
	for name := range m {
		if !slices.Contains(names, name) {
			return fmt.Errorf("unknown member %q", name)
		}
	}
	return nil
}

// member reads the named member with read, refusing one that is absent.
func member[T any](m map[string]json.RawMessage, name string, read func(json.RawMessage) (T, error)) (T, error) {
	raw, ok := m[name]
	if !ok {
		var zero T
		return zero, fmt.Errorf("no member %q", name)
	}
	v, err := read(raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// str reads a JSON string. null is refused: encoding/json would read it as
// the empty string.
func str(raw json.RawMessage) (string, error) {
	var s string
	if len(raw) == 0 || raw[0] != '"' {
		return "", errors.New("not a string")
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errors.New("not a string")
	}
	return s, nil
}

// array reads a JSON array, whose items are left for the caller. null is
// refused.
func array(raw json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' {
		return nil, errors.New("not an array")
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("not an array")
	}
	return items, nil
}

// object reads a JSON object's members.
func object(raw json.RawMessage) (map[string]json.RawMessage, error) {
	return members(raw, false)
}
