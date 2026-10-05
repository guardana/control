package notify

import (
	"bytes"
	"fmt"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// MaxDeliveredBytes bounds the delivered list: a list past it is refused,
// and so is a mark that would take it past, before its program runs.
const MaxDeliveredBytes = 64 << 20

// maxKeyLine is the longest line a mark writes, its newline included; a torn
// mark leaves less than that after the last newline.
var maxKeyLine = len(keyLine("fnd-" + strings.Repeat("0", 32) + ":" + longestVerdict()))

// key is a delivery's key: the finding's id and its verdict's name, which a
// receiver can read from the record it is handed.
func key(id string, verdict controlv1.FindingVerdict) string {
	return id + ":" + verdict.String()
}

// keyLine is key as the delivered list holds it, a JSON string on a line.
// A valid key holds no character JSON escapes.
func keyLine(k string) []byte { return []byte(`"` + k + `"` + "\n") }

// parseDelivered reads a delivered list: whole lines, each one key in its one
// spelling, then at most a tail a torn mark could have left, which end
// excludes. Anything else is ErrState, naming the line's byte offset.
func parseDelivered(data []byte) (keys []string, end int, err error) {
	end = bytes.LastIndexByte(data, '\n') + 1
	if !tornMark(data[end:]) {
		return nil, 0, fmt.Errorf("%w: the delivered list ends in bytes no mark leaves, at byte %d", ErrState, end)
	}
	for off := 0; off < end; {
		n := bytes.IndexByte(data[off:end], '\n')
		k, ok := unquote(data[off : off+n])
		if !ok {
			return nil, 0, fmt.Errorf("%w: the delivered list's line at byte %d is not a key", ErrState, off)
		}
		keys = append(keys, k)
		off += n + 1
	}
	return keys, end, nil
}

func unquote(line []byte) (string, bool) {
	inner, ok := bytes.CutPrefix(line, []byte(`"`))
	if !ok {
		return "", false
	}
	inner, ok = bytes.CutSuffix(inner, []byte(`"`))
	return string(inner), ok && validKey(string(inner))
}

// tornMark reports whether tail, which holds no newline, is the start of a
// line a mark writes.
func tornMark(tail []byte) bool {
	switch {
	case len(tail) == 0:
		return true
	case len(tail) >= maxKeyLine || tail[0] != '"':
		return false
	}
	if inner, closed := bytes.CutSuffix(tail[1:], []byte(`"`)); closed {
		return validKey(string(inner))
	}
	for _, c := range tail[1:] {
		if !keyChar(c) {
			return false
		}
	}
	return true
}

func keyChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-' || c == '_' || c == ':'
}

func validKey(k string) bool {
	id, verdict, ok := strings.Cut(k, ":")
	n, known := controlv1.FindingVerdict_value[verdict]
	hexID, prefixed := strings.CutPrefix(id, "fnd-")
	return ok && known && n != 0 && prefixed && lowerHex(hexID, 32)
}

// lowerHex reports whether s is n lowercase hex digits.
func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func longestVerdict() string {
	longest := ""
	for _, name := range controlv1.FindingVerdict_name {
		if len(name) > len(longest) {
			longest = name
		}
	}
	return longest
}
