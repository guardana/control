package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// The members each record has, as the contract lists them all.
var (
	recordMembers = map[string][]string{
		"header":    {"type", "format", "version", "file", "source", "query"},
		"event":     {"type", "offset", "cursor", "event"},
		"gap":       {"type", "offset", "cursor", "reason"},
		"duplicate": {"type", "offset", "event_id", "first_offset"},
		"trailer":   {"type", "next_cursor", "end_reached", "tail_bytes", "writer_held", "counts", "scanned_bytes", "dedup_scope"},
	}
	queryMembers = []string{"after", "limit", "max_bytes", "request", "run", "tenant", "project", "kind"}
	countMembers = []string{"event", "gap", "duplicate"}
)

type member struct {
	name  string
	value json.RawMessage
}

// object reads b as one JSON object and nothing after it, its members in the
// object's order, and refuses a member held twice: a decoder that keeps the
// last of two would let one record say two things.
func object(b []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var ms []member
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("member %q appears twice", name)
		}
		seen[name] = true
		ms = append(ms, member{name, value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("more follows the object")
	}
	return ms, nil
}

// only refuses the first member that is not one of known, saying when it is
// one of them in another case, which a decoder folding case would read as it.
func only(ms []member, known []string) error {
	for _, m := range ms {
		if slices.Contains(known, m.name) {
			continue
		}
		for _, k := range known {
			if strings.EqualFold(k, m.name) {
				return fmt.Errorf("member %q differs from %q only in case", m.name, k)
			}
		}
		return fmt.Errorf("member %q is not one the contract names", m.name)
	}
	return nil
}

// strictObject is object and only at once, for a value nested in a record.
func strictObject(b []byte, known []string) error {
	ms, err := object(b)
	if err != nil {
		return err
	}
	return only(ms, known)
}

func valueOf(ms []member, name string) (json.RawMessage, bool) {
	for _, m := range ms {
		if m.name == name {
			return m.value, true
		}
	}
	return nil, false
}

// strictHeader holds the header, and the query in it, to their members.
func strictHeader(b []byte) error {
	ms, err := object(b)
	if err == nil {
		err = only(ms, recordMembers["header"])
	}
	if err != nil {
		return fmt.Errorf("the first record is not a header: %w", err)
	}
	if q, ok := valueOf(ms, "query"); ok {
		if err := strictObject(q, queryMembers); err != nil {
			return fmt.Errorf("the header's query is not one the contract gives: %w", err)
		}
	}
	return nil
}

// isCursor reads a cursor's spelling as the contract gives it,
// v1:<first>:<offset>:<line>: each digest 64 lowercase hex digits and the
// offset a decimal above zero with no sign and no leading zero.
func isCursor(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] != "v1" || !isSum(parts[1]) || !isSum(parts[3]) {
		return false
	}
	offset := parts[2]
	if offset == "" || offset[0] < '1' || offset[0] > '9' {
		return false
	}
	_, err := strconv.ParseInt(offset, 10, 64)
	return err == nil
}

func isSum(s string) bool {
	return len(s) == 64 && !strings.ContainsFunc(s, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') })
}
