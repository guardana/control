package scenario

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// Error is a class of refusal, matched with errors.Is. They are constants, so
// no other code in the binary can reassign one and turn a refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// The classes of refusal. Every refusal of Read is a *Refusal wrapping one.
const (
	ErrName          Error = "scenario: the file name is not a scenario id"
	ErrTooLarge      Error = "scenario: the document is over its size bound"
	ErrSyntax        Error = "scenario: the document is not JSON"
	ErrTooDeep       Error = "scenario: the document nests past its bound"
	ErrTrailing      Error = "scenario: something follows the document"
	ErrNotUTF8       Error = "scenario: a string is not UTF-8"
	ErrSurrogate     Error = "scenario: a string escapes a lone surrogate"
	ErrNull          Error = "scenario: null is refused"
	ErrDuplicate     Error = "scenario: a member is repeated"
	ErrUnknownMember Error = "scenario: an unknown member"
	ErrMissing       Error = "scenario: a required member is missing"
	ErrKind          Error = "scenario: a kind this build does not read"
	ErrWrongType     Error = "scenario: a value of the wrong type"
	ErrBound         Error = "scenario: a value past its bound"
	ErrValue         Error = "scenario: a value outside its set"
	ErrStep          Error = "scenario: a step refers to a step it may not"
	ErrReference     Error = "scenario: a reference is not well formed"
	ErrNoEvidence    Error = "scenario: no call step expects an event"
	ErrOutput        Error = "scenario: an output cannot be put into the arguments"
)

// MaxQuoteBytes bounds the escaped text a refusal quotes from the document.
const MaxQuoteBytes = 64

// Refusal is one refused document: the member it is about, what class of
// refusal it is, and at most MaxQuoteBytes of the offending text, escaped so
// that it prints as printable ASCII.
type Refusal struct {
	Path   string
	Err    Error
	Detail string
	Quote  string
	Cut    bool
}

// Error renders the refusal on one line.
func (r *Refusal) Error() string {
	var b strings.Builder
	b.WriteString(string(r.Err))
	if r.Path != "" {
		b.WriteString(" at ")
		b.WriteString(r.Path)
	}
	if r.Detail != "" {
		b.WriteString(": ")
		b.WriteString(r.Detail)
	}
	if r.Quote != "" || r.Cut {
		b.WriteString(` "`)
		b.WriteString(r.Quote)
		b.WriteByte('"')
		if r.Cut {
			b.WriteString("...")
		}
	}
	return b.String()
}

// Unwrap returns the class, for errors.Is.
func (r *Refusal) Unwrap() error { return r.Err }

func refuse(p path, err Error, detail string) *Refusal {
	return &Refusal{Path: p.String(), Err: err, Detail: detail}
}

func refuseQuoting(p path, err Error, detail string, text []byte) *Refusal {
	r := refuse(p, err, detail)
	r.Quote, r.Cut = quote(text)
	return r
}

// quote escapes text into printable ASCII and keeps at most MaxQuoteBytes of
// it, never splitting an escape. A byte that is not UTF-8 is written \xNN, so
// the quote shows what the file holds rather than a replacement character.
func quote(text []byte) (string, bool) {
	var b strings.Builder
	for len(text) > 0 {
		r, width := utf8.DecodeRune(text)
		var piece string
		switch {
		case r == utf8.RuneError && width <= 1:
			piece = `\x` + hex2(text[0])
		case r == '"' || r == '\\':
			piece = `\` + string(r)
		case r >= 0x20 && r < 0x7f:
			piece = string(r)
		default:
			piece = strings.Trim(strconv.QuoteRuneToASCII(r), "'")
		}
		if b.Len()+len(piece) > MaxQuoteBytes {
			return b.String(), true
		}
		b.WriteString(piece)
		text = text[width:]
	}
	return b.String(), false
}

func hex2(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&0x0f]})
}
