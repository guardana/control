package approval

import (
	"fmt"
	"strings"

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
	// ErrReservedApproverID is a claimed approver id that begins with
	// AuthnPrefix as a reader would see it: in any letter case, or spelled
	// with a lookalike of a letter or of the colon.
	ErrReservedApproverID Error = "approval: an approver id may not begin with the reserved authn: prefix, which only the plane writes"
	// ErrAuthenticatedApprover is an issuer or a subject an authenticated
	// approver id cannot be built from.
	ErrAuthenticatedApprover Error = "approval: an authenticated approver id needs an issuer with no colon and a subject, each an identifier, within the approver id's bound"
)

// AuthenticatedApproverID builds "authn:<issuer>:<subject>" for an approver
// the plane authenticated. The issuer holds no colon and no lookalike of one,
// so the first two colons split the id the same way for every reader.
func AuthenticatedApproverID(issuer, subject string) (string, error) {
	if issuer == "" || subject == "" {
		return "", fmt.Errorf("%w: the issuer and the subject are both required", ErrAuthenticatedApprover)
	}
	for _, part := range []string{issuer, subject} {
		if err := contract.CheckIdentifier(part); err != nil {
			return "", fmt.Errorf("%w: %w", ErrAuthenticatedApprover, err)
		}
	}
	if strings.ContainsFunc(issuer, colonLike) {
		return "", fmt.Errorf("%w: the issuer holds a colon", ErrAuthenticatedApprover)
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

// spellsAuthnPrefix reports whether id begins with AuthnPrefix once marks,
// format characters and variation selectors are passed over, letters are
// compared without case and through their lookalikes, and the colon through
// its own.
func spellsAuthnPrefix(id string) bool {
	matched := 0
	for _, r := range id {
		if ignorable(r) {
			continue
		}
		want := AuthnPrefix[matched]
		if want == ':' {
			return colonLike(r)
		}
		if !letterLike(r, rune(want)) {
			return false
		}
		matched++
	}
	return false
}
