package contract

import (
	"fmt"
	"strconv"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// checkValues covers a value that is present, well typed, within its limit and
// still refused. The rules on map keys run in the walk, which is where keys are
// visited. In field-number order, like everything else, so the first failure
// is reproducible.
func checkValues(env *controlv1.ActionEnvelope) error {
	if err := checkDelegation(env); err != nil {
		return err
	}
	if err := hostProblem(env.GetDestination().GetHost()); err != nil {
		return &ValidationError{Field: "destination.host", Err: err}
	}
	if err := checkLabels(env.GetData()); err != nil {
		return err
	}
	return checkArguments(env.GetArguments())
}

// checkDelegation holds the chain to what ADR-0011 fixes about it: it starts at
// principal.id, each hop continues the one before, the last ends at agent.id,
// and every hop carries an expiry no earlier than its issue time. Linked hops
// can still describe two strangers, which is why both ends are anchored.
//
// Root outwards: read the other way, the planned "child exceeds parent" check
// is a no-op.
func checkDelegation(env *controlv1.ActionEnvelope) error {
	hops := env.GetDelegation()
	start, reason := env.GetPrincipal().GetId(), "does not start at principal.id"
	for i, hop := range hops {
		if err := checkHop(hop, "delegation["+strconv.Itoa(i)+"]", start, reason); err != nil {
			return err
		}
		start, reason = hop.GetTo(), "does not continue the previous hop"
	}
	if last := len(hops) - 1; last >= 0 && hops[last].GetTo() != env.GetAgent().GetId() {
		return &ValidationError{
			Field: "delegation[" + strconv.Itoa(last) + "].to",
			Err:   fmt.Errorf("%w: does not end at agent.id", ErrInvalidValue),
		}
	}
	return nil
}

// checkHop names both ends first: proto3 leaves an unnamed hop holding "" at
// each end, so a chain of those continues itself, and a pass over the link
// check would be a pass over a no-op.
func checkHop(hop *controlv1.Delegation, path, start, reason string) error {
	switch {
	case hop.GetFrom() == "":
		return &ValidationError{Field: path + ".from", Err: ErrMissingField}
	case hop.GetTo() == "":
		return &ValidationError{Field: path + ".to", Err: ErrMissingField}
	case hop.GetExpiresAt() == nil:
		// An absent expiry would outlast every present one, which is absence
		// being more permissive than presence (ADR-0002).
		return &ValidationError{Field: path + ".expires_at", Err: ErrMissingField}
	case expiresBeforeIssued(hop):
		return &ValidationError{Field: path + ".expires_at", Err: fmt.Errorf("%w: expires before it was issued", ErrInvalidValue)}
	case hop.GetFrom() != start:
		return &ValidationError{Field: path + ".from", Err: fmt.Errorf("%w: %s", ErrInvalidValue, reason)}
	}
	return nil
}

// expiresBeforeIssued compares the two as written. Both passed checkTimestamp
// in the walk, so the nanos are in range and the pair orders like an instant.
func expiresBeforeIssued(hop *controlv1.Delegation) bool {
	issued, expires := hop.GetIssuedAt(), hop.GetExpiresAt()
	if issued == nil {
		return false
	}
	if expires.GetSeconds() != issued.GetSeconds() {
		return expires.GetSeconds() < issued.GetSeconds()
	}
	return expires.GetNanos() < issued.GetNanos()
}

// checkLabels refuses SENSITIVITY_UNSPECIFIED as a label: a label that says
// nothing is not one, and ToxicFlow would have to read it as unknown.
func checkLabels(d *controlv1.DataLabels) error {
	for i, s := range d.GetSensitivities() {
		if s == controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED {
			return &ValidationError{
				Field: "data.sensitivities[" + strconv.Itoa(i) + "]",
				Err:   fmt.Errorf("%w: a label that says nothing", ErrInvalidValue),
			}
		}
	}
	return nil
}

// checkArguments holds the two claims Arguments makes about itself.
func checkArguments(a *controlv1.Arguments) error {
	// Only the shape: Validate does not hold the arguments. The receiver,
	// which does, compares the value and refuses a mismatch (ADR-0011).
	if hash := a.GetCanonicalHash(); hash != "" && !isSHA256Digest(hash) {
		return &ValidationError{
			Field: "arguments.canonical_hash",
			Err:   fmt.Errorf("%w: want sha256: and lowercase hex", ErrInvalidValue),
		}
	}
	// An empty profile means no redaction ran, so a preview beside one is raw
	// content on its way into evidence with nothing in the record saying so.
	if a.GetRedactedPreview() != "" && a.GetRedactionProfile() == "" {
		return &ValidationError{
			Field: "arguments.redacted_preview",
			Err:   fmt.Errorf("%w: names no redaction profile", ErrInvalidValue),
		}
	}
	return nil
}

// isSHA256Digest holds the case rule the contract states: a digest is compared
// for equality to authorize a call, so upper case is a different string.
func isSHA256Digest(s string) bool {
	const prefix, hexLen = "sha256:", 64
	hex, ok := strings.CutPrefix(s, prefix)
	if !ok || len(hex) != hexLen {
		return false
	}
	for _, r := range hex {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
