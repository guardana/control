package trailchain

import controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

// Where a request has got to. The zero value is the state before its first
// event, so a fresh walk needs no setup.
type chainState int

const (
	chainStart chainState = iota
	chainProposed
	chainDecided
	chainApprovalRequested
	chainApprovalDecided
	chainApprovalExpired
	chainStarted
	chainClosed
)

// chainStates is every state, in the order a trail passes them.
var chainStates = []chainState{
	chainStart, chainProposed, chainDecided, chainApprovalRequested,
	chainApprovalDecided, chainApprovalExpired, chainStarted, chainClosed,
}

// String names the state for the refusal message, which is read by someone
// holding a file and no code.
func (s chainState) String() string {
	switch s {
	case chainStart:
		return "the start of a trail"
	case chainProposed:
		return "ACTION_PROPOSED"
	case chainDecided:
		return "POLICY_DECIDED"
	case chainApprovalRequested:
		return "APPROVAL_REQUESTED"
	case chainApprovalDecided:
		return "a decided approval"
	case chainApprovalExpired:
		return "an approval window nobody answered"
	case chainStarted:
		return "ACTION_STARTED"
	case chainClosed:
		return "a closed action"
	default:
		return "an unnamed state"
	}
}

type stepResult int

const (
	stepAllowed stepResult = iota
	stepRefused
	// The kind is not one of the eleven this version knows. It is not refused:
	// see ErrIndeterminate.
	stepUnplaceable
)

// step reports the state after kind. The returned state is meaningless unless
// the result is stepAllowed.
//
// kind is never EVENT_KIND_UNSPECIFIED here: definiteDefect refuses that one
// ahead of the walk, because it is a defect from any state and has to be
// reported even where the walk has lost track of the state.
func (s chainState) step(kind controlv1.EventKind) (chainState, stepResult) {
	switch kind {
	case controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED:
		return chainProposed, allow(s == chainStart)
	case controlv1.EventKind_EVENT_KIND_POLICY_DECIDED:
		return chainDecided, allow(s == chainProposed)
	case controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED:
		// A window that closed unanswered may be opened again. Asking twice is
		// not a defect in the trail; what an unanswered window may not be
		// followed by is the action starting.
		return chainApprovalRequested, allow(s == chainDecided || s == chainApprovalExpired)
	case controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED:
		return chainApprovalDecided, allow(s == chainApprovalRequested)
	case controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED:
		return chainApprovalExpired, allow(s == chainApprovalRequested)
	case controlv1.EventKind_EVENT_KIND_ACTION_STARTED:
		return chainStarted, allow(s.mayStart())
	case controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED,
		controlv1.EventKind_EVENT_KIND_ACTION_FAILED:
		return chainClosed, allow(s == chainStarted)
	case controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED:
		return chainClosed, allow(s.mayBlock())
	case controlv1.EventKind_EVENT_KIND_FINDING_RAISED:
		return s, allow(s != chainStart)
	case controlv1.EventKind_EVENT_KIND_POLICY_RELOADED:
		return s, stepAllowed
	default:
		return s, stepUnplaceable
	}
}

// mayStart reports whether a verdict has been recorded, nothing has run yet,
// and any approval the verdict called for was answered. An expired window is
// not an answer: "nobody answered" is not authorization, and treating it as one
// would let a trail show an action running on an approval that never came.
func (s chainState) mayStart() bool {
	return s == chainDecided || s == chainApprovalDecided
}

// mayBlock reports whether the action can be refused from here. Everything
// mayStart covers, and an unanswered approval window, which is a reason to
// block and the only thing it is.
func (s chainState) mayBlock() bool {
	return s.mayStart() || s == chainApprovalExpired
}

func allow(ok bool) stepResult {
	if ok {
		return stepAllowed
	}
	return stepRefused
}

// Step is one edge of the trail's state machine, as step answers it.
type Step struct {
	// From and To are the states as refusals name them.
	From string
	Kind controlv1.EventKind
	// Declared is false for the one kind number no build declares.
	Declared bool
	To       string
	Allowed  bool
	// Unplaceable is the reading of a kind this version does not know: not
	// refused, see ErrIndeterminate. To is meaningless then.
	Unplaceable bool
}

// Steps evaluates step over every state and every declared kind but
// EVENT_KIND_UNSPECIFIED, which definiteDefect refuses ahead of the walk,
// plus one kind number no build declares. It is a listing for the reference
// page, derived from the validator's own function; nothing that validates
// reads it.
func Steps() []Step {
	kinds := controlv1.EventKind(0).Descriptor().Values()
	undeclared := controlv1.EventKind(0)
	for i := 0; i < kinds.Len(); i++ {
		if n := controlv1.EventKind(kinds.Get(i).Number()); n >= undeclared {
			undeclared = n + 1
		}
	}
	var steps []Step
	for _, from := range chainStates {
		for i := 0; i <= kinds.Len(); i++ {
			kind, declared := undeclared, false
			if i < kinds.Len() {
				kind, declared = controlv1.EventKind(kinds.Get(i).Number()), true
				if kind == controlv1.EventKind_EVENT_KIND_UNSPECIFIED {
					continue
				}
			}
			to, result := from.step(kind)
			steps = append(steps, Step{From: from.String(), Kind: kind, Declared: declared,
				To: to.String(), Allowed: result == stepAllowed, Unplaceable: result == stepUnplaceable})
		}
	}
	return steps
}
