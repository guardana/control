package policykey

import (
	"strings"
	"unicode"

	"github.com/guardana/control/internal/keytext"
)

// KeyTextWithheld stands in for the key text Printable takes out of a message.
const KeyTextWithheld = keytext.Withheld

// CheckArguments refuses, with ErrKeyText, a command line argument holding
// key text, a line break or another control character. A command runs it
// before its flag parsing, a file refusal or a success line could repeat such
// an argument.
func CheckArguments(args []string) error {
	for _, a := range args {
		if HoldsKeyText(a) || strings.IndexFunc(a, unicode.IsControl) >= 0 {
			return ErrKeyText
		}
	}
	return nil
}

// HoldsKeyText is keytext.Holds: whether s holds a PEM marker or the start of
// a key file's body line.
func HoldsKeyText(s string) bool { return keytext.Holds(s) }

// Printable is keytext.Printable: s with its key text withheld, quoted when
// it would break a line.
func Printable(s string) string { return keytext.Printable(s) }
