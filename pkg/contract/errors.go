package contract

import (
	"errors"
	"strconv"
)

// The refusals this package classifies, matched with errors.Is. The identity is
// the contract, not the text: a caller that compares message strings breaks on
// the first rewording, and errorlint rejects it besides.
//
// They classify most refusals, not all. A payload that does not decode at all
// fits none of them and carries the codec's own error instead, reachable
// through Unwrap, where it stays visible as something nobody classified rather
// than disappearing into a sentinel meaning "one of the reasons we did not
// enumerate". There is deliberately no catch-all.
var (
	ErrUnsupportedSchema = errors.New("contract: unsupported schema version")
	ErrUnknownField      = errors.New("contract: unknown field")
	ErrTooLarge          = errors.New("contract: size limit exceeded")
	ErrMissingField      = errors.New("contract: required field missing")
	ErrInvalidEnum       = errors.New("contract: invalid enum value")

	// ErrInvalidValue is narrow: the field is present, well typed and within
	// its size limit, and its value breaks a rule the contract states. A
	// reserved attribute key, a delegation chain whose hops do not link, a
	// digest that is not "sha256:" and lowercase hex. It is not the place for
	// a refusal that fits none of the others.
	ErrInvalidValue = errors.New("contract: invalid field value")
)

// errPrefix is the prefix every sentinel above carries.
const errPrefix = "contract: "

// ValidationError names the field that failed and wraps the reason it failed.
//
// It is used by pointer. The methods take a pointer receiver, so only
// *ValidationError satisfies error, and errors.As is given the address of a
// pointer:
//
//	var ve *contract.ValidationError
//	if errors.As(err, &ve) && errors.Is(ve, contract.ErrTooLarge) {
//		refuse(ve.Field)
//	}
//
// Field is the wire path of the field, "arguments.canonical_hash", so it names
// what the producer sent rather than a Go field name. It is empty when the
// refusal is about the message as a whole.
//
// Field never carries caller data. A path into a map or a repeated field uses
// the position, "principal.attributes[3]" and "delegation[1].to", never the key
// or the value found there: a caller that could choose part of this string
// could choose part of every record written about it, and this project's
// product is evidence. The set of paths is finite and comes from the schema.
// Error quotes it anyway, because a rule that only a reviewer enforces needs a
// backstop that runs.
//
// Err is a sentinel above wherever one fits. A failure with no sentinel, a
// payload that does not decode at all being the one that has none, wraps the
// underlying error instead, so a caller reaches the field with errors.As before
// it classifies with errors.Is.
type ValidationError struct {
	Field string
	Err   error
}

// Error renders the field and the reason, each as a quoted Go string, and never
// returns more than one line.
//
// Both parts are quoted because neither is fully this package's own text. The
// field is schema-derived but the rule that keeps caller data out of it is a
// rule, not a type; the reason is whatever error was wrapped, including a
// codec's message about input someone else chose. Rendered raw, a newline in
// either produces a second line that is a syntactically perfect record of a
// refusal that never happened, in a project whose product is evidence.
//
// The reason is quoted whole and nothing is trimmed off it. Trimming this
// package's prefix would render an unclassified error identically to the
// sentinel whose text it resembles, while errors.Is says they are different.
func (e *ValidationError) Error() string {
	if e == nil {
		// A typed nil in an error interface is a caller bug. It still reads as
		// a refusal to everything downstream, which is the safe direction, so
		// it has to print rather than crash whatever is enforcing.
		return strconv.Quote(errPrefix + "invalid: nil *ValidationError")
	}
	// A value built by hand can carry no cause, and printing it may not panic.
	reason := errPrefix + "invalid"
	if e.Err != nil {
		reason = e.Err.Error()
	}
	if e.Field == "" {
		return strconv.Quote(reason)
	}
	return strconv.Quote(e.Field) + ": " + strconv.Quote(reason)
}

// Unwrap exposes the sentinel to errors.Is, and the cause to a caller that
// wraps this error further. Nil-safe for the same reason Error is.
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// codecError is a refusal by the Protobuf runtime, rendered without the
// runtime's text. protojson quotes its input back, a field name, an enum name
// or a whole value of any length, and a refusal is written into evidence. The
// runtime also words its errors differently between builds on purpose, so the
// same bytes would not record the same reason twice. Its error stays reachable
// through Unwrap, for a caller that chooses to read it.
type codecError struct {
	reason string
	cause  error
}

func (e *codecError) Error() string { return e.reason }

func (e *codecError) Unwrap() error { return e.cause }
