package contract

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// CheckMapKey refuses a string no key of the envelope's maps may hold: one the
// identifier rule refuses, or one whose first nine code points fold to
// "reserved." (ADR-0011). Validate holds every key of principal.attributes,
// resource.labels and context.budgets to it. It is exported so that anything
// else that names a key, the policy parser first, holds it to this rule rather
// than to a second one (docs/contracts.md, "Decoding and validation"): a rule
// that names a key no envelope can carry never matches, and a DENY on it never
// fires.
//
// Two keys that fold together are a relation between keys, not a property of
// one, and stay the walk's own rule. Like CheckIdentifier it checks what a
// value holds, not whether there is one: "" passes. The refusal is a
// *ValidationError with no Field, wrapping ErrInvalidValue, and never repeats
// the key.
func CheckMapKey(key string) error {
	if err := mapKeyProblem(key); err != nil {
		return &ValidationError{Err: err}
	}
	return nil
}

// mapKeyProblem is CheckMapKey's rule without the wrapper, so the walk can name
// the entry it found the key in.
func mapKeyProblem(key string) error {
	if err := identifierProblem(key); err != nil {
		return err
	}
	// An adapter that could set any key could grant authority by translation,
	// so this namespace is not an adapter's to write, in any map.
	if reservedPrefixFolds(key) {
		return errReservedKey
	}
	return nil
}

// errReservedKey is one value so that the walk can tell the namespace refusal
// from a refusal of the key's spelling: it names the map entry for the first
// and the key for the second.
var errReservedKey = fmt.Errorf("%w: reserved key namespace", ErrInvalidValue)

// Wire semantics, not the product name: a rename must not legalise a refused
// key. Matched by folding a key's first nine code points (ADR-0011).
const reservedKeyPrefix = "reserved."

// reservedPrefixFolds reports whether the first nine code points of s fold to
// "reserved." under simple case folding, the relation ADR-0011 compares keys
// by, so that a consumer that folds or upper-cases keys finds no reserved key
// this package accepted. Code points rather than bytes: U+017F folds to s and
// takes two bytes, so a nine-byte window misses "re" U+017F "erved.x".
func reservedPrefixFolds(s string) bool {
	want, seen := utf8.RuneCountInString(reservedKeyPrefix), 0
	for i := range s {
		if seen == want {
			return strings.EqualFold(s[:i], reservedKeyPrefix)
		}
		seen++
	}
	return seen == want && strings.EqualFold(s, reservedKeyPrefix)
}
