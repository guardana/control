package approval

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/guardana/control/pkg/contract"
)

// AuthnPrefix begins the approver id of an approver the plane authenticated.
// Only AuthenticatedApproverID spells it; every approver id a person or a file
// supplies is held to CheckClaimedApproverID, which refuses it.
const AuthnPrefix = "authn:"

// MaxApproverIDBytes bounds an approver id, claimed or authenticated, so both
// cost a record and a listing the same.
const MaxApproverIDBytes = 128

// Error is a refusal of this package's own. Its values are constants, so a
// sentinel can never be nil and read as success.
type Error string

func (e Error) Error() string { return string(e) }

const (
	// ErrReservedApproverID is a claimed approver id that could read as
	// beginning with AuthnPrefix, as spellsAuthnPrefix decides it.
	ErrReservedApproverID Error = "approval: an approver id may not begin with the reserved authn: prefix, which only the plane writes; a character outside ASCII counts as a letter of it or as its colon"
	// ErrAuthenticatedApprover is an issuer or a subject an authenticated
	// approver id cannot be built from.
	ErrAuthenticatedApprover Error = "approval: an authenticated approver id needs an issuer of printable ASCII with no colon and a subject, each an identifier, within the approver id's bound"
)

// AuthenticatedApproverID builds "authn:<issuer>:<subject>" for an approver
// the plane authenticated. The issuer is printable ASCII with no colon, so it
// holds no lookalike of one either and the first two colons split the id the
// same way for every reader.
func AuthenticatedApproverID(issuer, subject string) (string, error) {
	if issuer == "" || subject == "" {
		return "", fmt.Errorf("%w: the issuer and the subject are both required", ErrAuthenticatedApprover)
	}
	for _, part := range []string{issuer, subject} {
		if err := contract.CheckIdentifier(part); err != nil {
			return "", fmt.Errorf("%w: %w", ErrAuthenticatedApprover, err)
		}
	}
	if strings.ContainsFunc(issuer, func(r rune) bool { return r < ' ' || r > '~' || r == ':' }) {
		return "", fmt.Errorf("%w: the issuer holds a colon or a character outside printable ASCII", ErrAuthenticatedApprover)
	}
	id := AuthnPrefix + issuer + ":" + subject
	if len(id) > MaxApproverIDBytes {
		return "", fmt.Errorf("%w: %d bytes, limit %d", ErrAuthenticatedApprover, len(id), MaxApproverIDBytes)
	}
	return id, nil
}

// CheckClaimedApproverID refuses a claimed approver id that a reader could
// take for an authenticated one. It checks nothing else: the bounds and the
// identifier rule are the store's.
func CheckClaimedApproverID(id string) error {
	if spellsAuthnPrefix(id) {
		return ErrReservedApproverID
	}
	return nil
}

// spellsAuthnPrefix reports whether the first six characters a reader sees
// in id could read as AuthnPrefix. An ASCII character is compared without
// case. Nothing in the standard library says what any other character looks
// like, so each one, a spacing mark included, counts as whatever letter or
// colon its position holds. The cost is that a name whose first five
// characters lie outside ASCII, followed by a colon or a sixth such
// character, is refused: "Борис:x" is one, while "zoë" is not. A renderer
// may lay out any run of an id that holds a right-to-left character first,
// so in such an id the six may stand anywhere.
func spellsAuthnPrefix(id string) bool {
	var window [len(AuthnPrefix)]rune
	seen := 0
	rtl, inside := false, false
	for _, r := range id {
		rtl = rtl || rightToLeft(r)
		if unseen(r) {
			continue
		}
		copy(window[:], window[1:])
		window[len(window)-1] = r
		seen++
		if seen >= len(window) && readsAsPrefix(window) {
			if seen == len(window) {
				return true
			}
			inside = true
		}
	}
	return inside && rtl
}

func readsAsPrefix(window [len(AuthnPrefix)]rune) bool {
	for i, r := range window {
		if r <= unicode.MaxASCII && unicode.ToLower(r) != rune(AuthnPrefix[i]) {
			return false
		}
	}
	return true
}

// unseen is a code point a reader does not see as a character of its own: a
// mark that takes no space, a format character, a variation selector or
// another default-ignorable code point (the Hangul fillers among them), white
// space, a separator, or the blank braille pattern.
func unseen(r rune) bool {
	return r == 0x2800 || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Z, unicode.White_Space,
		unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point)
}

// rightToLeft reports whether r lies in a block whose characters are laid out
// right to left, or is a control that opens such a run. The standard library
// carries no bidirectional classes, so the blocks are named whole.
func rightToLeft(r rune) bool {
	switch {
	case r >= 0x0590 && r <= 0x08ff, r >= 0xfb1d && r <= 0xfdff, r >= 0xfe70 && r <= 0xfefc,
		r >= 0x10800 && r <= 0x10fff, r >= 0x1e800 && r <= 0x1efff:
		return true
	}
	return r == 0x200f || r == 0x202b || r == 0x202e || r == 0x2067
}
