package coverage

// Error is a refusal by this package, matched with errors.Is.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

const (
	// ErrInventory is an inventory this package refuses.
	ErrInventory Error = "coverage: inventory refused"
	// ErrExport is an evidence export that is not one this package reads.
	ErrExport Error = "coverage: evidence export refused"
	// ErrInput is an input Map cannot state coverage from.
	ErrInput Error = "coverage: input refused"
)

// State is what covers a path, ordered from the weakest. The zero value is
// NotCovered, so a state nobody set never claims coverage.
type State uint8

const (
	// NotCovered is a path nothing below covers.
	NotCovered State = iota
	// Unknown is a path whose named source is present but past its heartbeat
	// or never heard, so nothing can say what happened.
	Unknown
	// Observed is a path a live source has observations of.
	Observed
	// Decided is a path a plane decides without enforcing what it decides.
	Decided
	// Enforced is a path every plane that classifies it enforces.
	Enforced
)

var stateNames = [...]string{NotCovered: "not covered", Unknown: "unknown", Observed: "observed",
	Decided: "decided, not enforced", Enforced: "enforced"}

// String is the state as a line prints it.
func (s State) String() string {
	if int(s) < len(stateNames) {
		return stateNames[s]
	}
	return "not covered"
}

// Join is what the evidence says of one observation beside a path a plane
// decides. The zero value is JoinNotChecked: a join nobody checked claims
// neither a pass through the plane nor a call around it.
type Join uint8

const (
	// JoinNotChecked is a join the evidence given cannot answer.
	JoinNotChecked Join = iota
	// JoinAround is whole evidence covering the observation's time that holds
	// no proposal it joins: a call around the plane.
	JoinAround
	// Joined is an observation a proposal of a plane counting for the path
	// joins, in a mode at least as strong as the path's state.
	Joined
)

// String is the join as a line prints it.
func (j Join) String() string {
	switch j {
	case JoinAround:
		return "a call around the plane"
	case Joined:
		return "joined"
	}
	return "not checked"
}
