package evidence

import controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

// ChainStep is one edge of the trail's state machine, as step answers it.
type ChainStep struct {
	// From and To are the states as refusals name them.
	From string
	Kind controlv1.EventKind
	// Declared is false for the one kind number no build declares.
	Declared bool
	To       string
	Allowed  bool
	// Unplaceable is the reading of a kind this version does not know: not
	// refused, see ErrChainIndeterminate. To is meaningless then.
	Unplaceable bool
}

// chainStates is every state, in the order a trail passes them.
var chainStates = []chainState{
	chainStart, chainProposed, chainDecided, chainApprovalRequested,
	chainApprovalDecided, chainApprovalExpired, chainStarted, chainClosed,
}

// ChainSteps evaluates step over every state and every declared kind but
// EVENT_KIND_UNSPECIFIED, which definiteDefect refuses ahead of the walk,
// plus one kind number no build declares. It is a listing for the reference
// page, derived from the validator's own function; nothing that validates
// reads it.
func ChainSteps() []ChainStep {
	kinds := controlv1.EventKind(0).Descriptor().Values()
	undeclared := controlv1.EventKind(0)
	for i := 0; i < kinds.Len(); i++ {
		if n := controlv1.EventKind(kinds.Get(i).Number()); n >= undeclared {
			undeclared = n + 1
		}
	}
	var steps []ChainStep
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
			steps = append(steps, ChainStep{From: from.String(), Kind: kind, Declared: declared,
				To: to.String(), Allowed: result == stepAllowed, Unplaceable: result == stepUnplaceable})
		}
	}
	return steps
}
