package core

// Error is a refusal by New, matched with errors.Is. They are constants, so no
// other code in the binary can reassign one and turn a refusal into a pass.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// New's refusals, one per check.
const (
	// ErrMode is any enforcement mode but ENFORCE: a declared one, which is
	// planned; the zero value; or a number this build does not declare.
	ErrMode Error = "core: only the ENFORCE mode is implemented"
	// ErrNoClock is a nil clock. The kernel reads no clock of its own.
	ErrNoClock Error = "core: no clock"
	// ErrNoIDSource is a nil decision id source.
	ErrNoIDSource Error = "core: no decision id source"
	// ErrApplicable is an applicable obligation type outside the catalogue.
	ErrApplicable Error = "core: an applicable obligation type is not in the catalogue"
	// ErrMaxStale is an operator's staleness budget that is not positive.
	ErrMaxStale Error = "core: the staleness budget is not positive"
	// ErrDecisionPoint is a decision point identifier a decision could not
	// carry: over the string bound, one the identifier rule refuses, or one
	// holding `@`, `?` or `#`, where a credential could hide.
	ErrDecisionPoint Error = "core: the decision point identifier cannot be carried on a decision"
)
