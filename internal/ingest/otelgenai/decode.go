package otelgenai

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

var (
	errSyntax    = errors.New("unexpected token")
	errMember    = errors.New("unknown member")
	errRepeated  = errors.New("member repeated")
	errKey       = errors.New("attribute key repeated")
	errNumber    = errors.New("not an integer in range")
	errTwoValues = errors.New("value holds two members")
	errDepth     = errors.New("value nested too deep")
	errEncoding  = errors.New("line is not UTF-8")
	errTrailing  = errors.New("more than one value on the line")
	errElements  = errors.New("line holds too many elements")
)

// maxValueDepth bounds how far array and key-value list values nest, so a
// line cannot drive the reader's recursion as deep as its length allows.
const maxValueDepth = 32

// MaxLineElements bounds the array elements one line may hold, a line over
// it being refused whole, and separately the messages, parts and members its
// messages strings may hold, a string over what is left being unparsed. A
// line at the bound averages 64 bytes an element, about a short attribute,
// and the reader keeps nothing of an attribute it does not copy, so a line's
// cost stays within a small multiple of its length.
const MaxLineElements = MaxLineBytes / 64

// budget is what is left of one of a line's two MaxLineElements.
type budget struct{ left int }

func newBudget() *budget { return &budget{left: MaxLineElements} }

func (b *budget) take() error {
	if b.left <= 0 {
		return errElements
	}
	b.left--
	return nil
}

// reader walks one token stream. Every value is read by a function that
// knows its type, so a null, a wrong type or a stray member is an error
// rather than a default.
type reader struct {
	dec    *json.Decoder
	budget *budget
	// parts is the budget of the messages strings of the line, and runKey
	// the descriptor's run attribute, which attributes() keeps.
	parts  *budget
	runKey string
}

func newReader(src io.Reader, b *budget) *reader {
	dec := json.NewDecoder(src)
	dec.UseNumber()
	return &reader{dec: dec, budget: b}
}

func (r *reader) delim(want json.Delim) error {
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("%w: want %v", errSyntax, want)
	}
	return nil
}

// object reads one object, handing each member's name to member, which must
// consume the value. A name seen twice is refused.
func (r *reader) object(member func(name string) error) error {
	if err := r.delim('{'); err != nil {
		return err
	}
	var seen names
	for r.dec.More() {
		tok, err := r.dec.Token()
		if err != nil {
			return err
		}
		name, ok := tok.(string)
		if !ok {
			return errSyntax
		}
		if !seen.add(name) {
			return fmt.Errorf("%w: %q", errRepeated, name)
		}
		if err := member(name); err != nil {
			return err
		}
	}
	return r.delim('}')
}

// names is the set of names one object or attribute list has shown. Most
// hold a few, so the first ones are kept without a map.
type names struct {
	few  [16]string
	n    int
	many map[string]struct{}
}

func (s *names) add(name string) bool {
	for _, seen := range s.few[:s.n] {
		if seen == name {
			return false
		}
	}
	if _, ok := s.many[name]; ok {
		return false
	}
	if s.n < len(s.few) {
		s.few[s.n] = name
		s.n++
		return true
	}
	if s.many == nil {
		s.many = map[string]struct{}{}
	}
	s.many[name] = struct{}{}
	return true
}

// array reads one array, each element taken from the line's budget.
func (r *reader) array(elem func() error) error {
	if err := r.delim('['); err != nil {
		return err
	}
	for r.dec.More() {
		if err := r.budget.take(); err != nil {
			return err
		}
		if err := elem(); err != nil {
			return err
		}
	}
	return r.delim(']')
}

func (r *reader) str() (string, error) {
	tok, err := r.dec.Token()
	if err != nil {
		return "", err
	}
	s, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("%w: want a string", errSyntax)
	}
	return s, nil
}

func (r *reader) boolean() error {
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	if _, ok := tok.(bool); !ok {
		return fmt.Errorf("%w: want a boolean", errSyntax)
	}
	return nil
}

// integerText reads a 64-bit integer field, which OTLP/JSON writes as a
// decimal string or a number, and returns its digits.
func (r *reader) integerText(numberOnly bool) (string, error) {
	tok, err := r.dec.Token()
	if err != nil {
		return "", err
	}
	switch v := tok.(type) {
	case json.Number:
		return string(v), nil
	case string:
		if !numberOnly {
			return v, nil
		}
	}
	return "", errNumber
}

func (r *reader) uint(bits int) (uint64, error) {
	s, err := r.integerText(false)
	if err != nil {
		return 0, err
	}
	if !decimal(s, false) {
		return 0, errNumber
	}
	n, err := strconv.ParseUint(s, 10, bits)
	if err != nil {
		return 0, errNumber
	}
	return n, nil
}

func (r *reader) int(bits int, numberOnly bool) (int64, error) {
	s, err := r.integerText(numberOnly)
	if err != nil {
		return 0, err
	}
	if !decimal(s, true) {
		return 0, errNumber
	}
	n, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		return 0, errNumber
	}
	return n, nil
}

// enum reads an enum, which OTLP/JSON writes as an integer, never a name.
func (r *reader) enum() (int64, error) { return r.int(32, true) }

// decimal reports digits only, with a leading minus when signed allows it:
// no fraction, exponent, plus sign or space.
func decimal(s string, signed bool) bool {
	if signed && len(s) > 1 && s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (r *reader) double() error {
	tok, err := r.dec.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case json.Number:
		return nil
	case string:
		if v == "NaN" || v == "Infinity" || v == "-Infinity" {
			return nil
		}
	}
	return fmt.Errorf("%w: want a double", errSyntax)
}

func (r *reader) base64() error {
	s, err := r.str()
	if err != nil {
		return err
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if _, err := enc.DecodeString(s); err == nil {
			return nil
		}
	}
	return fmt.Errorf("%w: want base64", errSyntax)
}

// member decodes the value of one named member into v.
type member[T any] func(r *reader, v *T) error

// decode reads an object whose members are exactly those the table names.
func decode[T any](r *reader, table map[string]member[T], v *T) error {
	return r.object(func(name string) error {
		f, ok := table[name]
		if !ok {
			return fmt.Errorf("%w: %q", errMember, name)
		}
		return f(r, v)
	})
}

func skipString[T any](r *reader, _ *T) error { _, err := r.str(); return err }
func skipUint32[T any](r *reader, _ *T) error { _, err := r.uint(32); return err }
func skipUint64[T any](r *reader, _ *T) error { _, err := r.uint(64); return err }
func skipEnum[T any](r *reader, _ *T) error   { _, err := r.enum(); return err }

func skipStrings[T any](r *reader, _ *T) error {
	return r.array(func() error { _, err := r.str(); return err })
}

// decodeLine reads one TracesData line. Any refusal is the whole line's.
func decodeLine(line []byte, runKey string) ([]*resourceSpans, error) {
	if !utf8.Valid(line) {
		return nil, errEncoding
	}
	r := newReader(bytes.NewReader(line), newBudget())
	r.parts, r.runKey = newBudget(), runKey
	var td tracesData
	if err := decode(r, tracesDataMembers, &td); err != nil {
		return nil, err
	}
	if !r.atEnd() {
		return nil, errTrailing
	}
	return td.resources, nil
}

func (r *reader) atEnd() bool {
	_, err := r.dec.Token()
	return errors.Is(err, io.EOF)
}
