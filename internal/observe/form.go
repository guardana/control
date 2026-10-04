package observe

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ErrLineForm reports a line that is not spelled the way MarshalLine spells
// one.
var ErrLineForm = errors.New("observe: line not in the writer's form")

// memberNames are the JSON names of every field a record can carry; mapNames
// are those of its map fields, whose own members are keys, not names.
var memberNames, mapNames = recordNames()

// CheckLineForm refuses a line, with or without its one newline, that is not
// spelled as MarshalLine spells a line: whitespace or a byte beyond ASCII
// outside a string, a member not named by its field's JSON name, a raw rune
// the writer escapes, and an escape the writer does not write. It judges
// spelling only: a line it passes still needs UnmarshalLine, and the two
// together hold a line to the writer's form without encoding it again.
func CheckLineForm(line []byte) error {
	s := formScan{b: bytes.TrimSuffix(line, []byte("\n")), colon: -1}
	for i := 0; i < len(s.b); {
		c := s.b[i]
		switch {
		case c == '"':
			end, err := s.member(i)
			if err != nil {
				return err
			}
			i = end
			continue
		case c == '{':
			s.open(i)
		case c == '}':
			s.close()
		case c <= ' ' || c >= 0x7f:
			return fmt.Errorf("%w: byte %#02x outside a string at offset %d", ErrLineForm, c, i)
		}
		i++
	}
	return nil
}

// formScan walks one line. inMap holds, for each object open at the walk,
// whether its members are a map's keys; key and colon are the last member
// name the walk met and the offset of the colon after it.
type formScan struct {
	b     []byte
	inMap []bool
	key   string
	colon int
}

// member checks the string opening at offset i and, when a colon follows
// it, its spelling as a member name, and returns the offset after it.
func (s *formScan) member(i int) (int, error) {
	end, err := stringEnd(s.b, i)
	if err != nil {
		return 0, err
	}
	if end == len(s.b) || s.b[end] != ':' {
		return end, nil
	}
	name := string(s.b[i+1 : end-1])
	if !s.mapOpen() && !memberNames[name] {
		return 0, fmt.Errorf("%w: member %s is not a field's JSON name", ErrLineForm, quoted(name, maxIdentifierBytes))
	}
	s.key, s.colon = name, end
	return end, nil
}

func (s *formScan) open(i int) {
	isMap := i > 0 && s.colon == i-1 && mapNames[s.key] && !s.mapOpen()
	s.inMap = append(s.inMap, isMap)
}

func (s *formScan) close() {
	if len(s.inMap) > 0 {
		s.inMap = s.inMap[:len(s.inMap)-1]
	}
}

func (s *formScan) mapOpen() bool { return len(s.inMap) > 0 && s.inMap[len(s.inMap)-1] }

// stringEnd returns the offset just past the string that opens at b[i],
// refusing what the writer would have escaped and an escape it does not
// write.
func stringEnd(b []byte, i int) (int, error) {
	for j := i + 1; j < len(b); {
		c := b[j]
		switch {
		case c == '"':
			return j + 1, nil
		case c == '\\':
			n, err := escapeLen(b[j:])
			if err != nil {
				return 0, fmt.Errorf("%w: offset %d: %w", ErrLineForm, j, err)
			}
			j += n
		case c < ' ' || c == 0x7f:
			return 0, fmt.Errorf("%w: raw byte %#02x in a string at offset %d", ErrLineForm, c, j)
		case c < utf8.RuneSelf:
			j++
		default:
			r, size := utf8.DecodeRune(b[j:])
			if (r == utf8.RuneError && size == 1) || needsEscape(r) {
				return 0, fmt.Errorf("%w: raw %U in a string at offset %d", ErrLineForm, r, j)
			}
			j += size
		}
	}
	return 0, fmt.Errorf("%w: a string that does not close", ErrLineForm)
}

// escapeLen returns the length of the escape that opens b. protojson writes
// a quote, a backslash and the five controls with a short form as two
// characters and every other control below a space as four lowercase hex
// digits; escapeLine writes its set in four lowercase hex digits too.
func escapeLen(b []byte) (int, error) {
	switch {
	case len(b) < 2:
		return 0, errors.New("an escape cut short")
	case b[1] != 'u' && strings.IndexByte(`"\bfnrt`, b[1]) >= 0:
		return 2, nil
	case b[1] != 'u':
		return 0, fmt.Errorf("the escape %q", b[:2])
	}
	r, ok := escapedRune(b[2:])
	switch {
	case !ok:
		return 0, errors.New("an escape not of four lowercase hex digits")
	case r < ' ' && !strings.ContainsRune("\b\f\n\r\t", r), needsEscape(r):
		return 6, nil
	}
	return 0, fmt.Errorf("an escape of %U, which the writer writes otherwise", r)
}

// escapedRune reads four lowercase hex digits at the start of b.
func escapedRune(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var r rune
	for _, c := range b[:4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// recordNames walks the record's messages, the well-known types aside, which
// protojson writes as strings.
func recordNames() (names, maps map[string]bool) {
	names, maps = map[string]bool{}, map[string]bool{}
	seen := map[protoreflect.FullName]bool{}
	var walk func(protoreflect.MessageDescriptor)
	walk = func(md protoreflect.MessageDescriptor) {
		if seen[md.FullName()] || md.ParentFile().Package() == "google.protobuf" {
			return
		}
		seen[md.FullName()] = true
		fields := md.Fields()
		for i := range fields.Len() {
			fd := fields.Get(i)
			names[fd.JSONName()] = true
			if fd.IsMap() {
				maps[fd.JSONName()] = true
				fd = fd.MapValue()
			}
			if fd.Message() != nil {
				walk(fd.Message())
			}
		}
	}
	walk((&observev1.Record{}).ProtoReflect().Descriptor())
	return names, maps
}
