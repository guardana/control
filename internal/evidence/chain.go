package evidence

import (
	"errors"
	"fmt"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

var (
	// ErrChainBroken reports a definite defect: a link that does not join, an
	// event belonging to another request, project or tenant, an identifier or
	// a mode an event has to carry and does not, an identifier longer than the
	// contract lets a string be, or a step the order below does not allow.
	ErrChainBroken = errors.New("evidence: chain broken")

	// ErrChainIndeterminate reports that the sequence carries an event kind
	// this version cannot place, or an enforcement mode it does not declare,
	// so the trail could not be read either way. event.proto states that an
	// undeclared kind is INDETERMINATE to a reader and never an error, which is
	// what lets a later minor version add one, and common.proto states the
	// same of every undeclared enum number; reporting such a sequence as well
	// formed would claim a reading nothing performed, and reporting it as
	// broken would blame a producer for being newer.
	//
	// It is the weaker answer and it never hides the stronger one. Everything
	// that is a defect whatever the unplaceable kind turns out to mean is
	// reported as ErrChainBroken instead, wherever in the sequence it sits: the
	// links, the identifiers, an event that names no kind, a second proposal.
	// What the undetermined reading does cover is the steps that depend on the
	// state the walk lost, and only those.
	ErrChainIndeterminate = errors.New("evidence: chain indeterminate")
)

// ValidateChain reports whether events are one coherent account of one request.
//
// It answers whether the trail is well formed. It says nothing about whether
// the trail is true: prev_event_id is an ordering link, so a gap shows and an
// altered record does not. prev_event_digest, field 31, is declared for the
// digest link that would change that, and nothing here reads it (ADR-0011; the
// hash chain is planned, ADR-0004).
//
// The order it accepts, per request_id, a bracketed group being optional:
//
//	ACTION_PROPOSED -> POLICY_DECIDED
//	  -> [APPROVAL_REQUESTED -> APPROVAL_DECIDED]
//	  -> ( ACTION_STARTED -> (ACTION_COMPLETED | ACTION_FAILED) )
//	   | ACTION_BLOCKED
//
// APPROVAL_EXPIRED closes an approval window that nobody answered. It may be
// followed by ACTION_BLOCKED, or by another APPROVAL_REQUESTED, and not by
// ACTION_STARTED: event.proto keeps it distinct from APPROVAL_DECIDED because
// "nobody answered" and "somebody said no" are different facts about an
// operator's controls, and a grammar that let the action run after either would
// spend that distinction on nothing.
//
// FINDING_RAISED sits outside that sequence and is accepted at any point after
// ACTION_PROPOSED, including after the last one: the detector plane is
// asynchronous and off the decision path, so a finding is caused by a detector
// finishing rather than by this request progressing. It is refused before
// ACTION_PROPOSED, where it would annotate an action the trail has not
// mentioned. POLICY_RELOADED is accepted anywhere within the sequence,
// including as the first event, because a bundle reload is caused by an
// operator and not by this request.
//
// A sequence may end in any state. An in-flight request is a prefix of a trail,
// not a broken one; what it may not do is take a step the order does not allow.
//
// Beside the order, every event carries the trail's scope: the request_id,
// project_id and tenant_id of event 0, none of them empty, because request_id
// is unique only within a project. Every event names an enforcement mode.
// ACTION_STARTED, ACTION_COMPLETED and ACTION_FAILED each name an execution,
// and the event that closes the action names the one that started it. No
// identifier it reads is longer than contract.MaxStringBytes, the contract's
// bound on a string. A mode this version does not declare is read the way a
// kind it cannot place is: undetermined, and never in front of a definite
// defect.
//
// It refuses an empty slice, and a sequence in which nothing proposes an
// action. An empty trail is the shape a dropped read has, a trail of nothing
// but reloads is the shape a read that dropped the request has, and reporting
// either as well formed is the false green this project looks for.
//
// It does not order events by occurred_at, does not read payloads, and does not
// look at schema_version. Not reading payloads has a cost worth stating: an
// approval carries its outcome in its payload, so a trail in which the approver
// said no and the action ran anyway is well formed here. A nil result is
// therefore not a verification and must not be reported as one: not "verified",
// not "intact", not "untampered".
func ValidateChain(events []*controlv1.Event) error {
	if len(events) == 0 {
		return fmt.Errorf("%w: no events", ErrChainBroken)
	}
	if err := checkLinks(events); err != nil {
		return err
	}
	if err := checkOrder(events); err != nil {
		return err
	}
	return checkModes(events)
}

// checkLinks holds the rules that do not depend on an event's kind, so they
// still apply to a sequence whose order this version cannot establish: the
// length of every identifier, the scope every event shares, the mode each one
// names, and the links.
func checkLinks(events []*controlv1.Event) error {
	scope, err := scopeOf(events[0])
	if err != nil {
		return err
	}
	seen := make(map[string]int, len(events))
	prev := ""
	for i, event := range events {
		if event == nil {
			return fmt.Errorf("%w: event %d is nil", ErrChainBroken, i)
		}
		if err := lengthDefect(i, event); err != nil {
			return err
		}
		if err := scope.holds(i, event); err != nil {
			return err
		}
		if err := modeDefect(i, event); err != nil {
			return err
		}
		if event.GetEventId() == "" {
			// Without an id the link below cannot be read: an empty id and an
			// empty prev_event_id are the same bytes as the head of a trail.
			return fmt.Errorf("%w: event %d carries no event_id", ErrChainBroken, i)
		}
		if first, dup := seen[event.GetEventId()]; dup {
			// A repeating id generator makes every link join, including an
			// event to itself, so the link check alone would report nothing.
			return fmt.Errorf("%w: events %d and %d share event_id %s",
				ErrChainBroken, first, i, quoteID(event.GetEventId()))
		}
		seen[event.GetEventId()] = i
		if event.GetPrevEventId() != prev {
			return fmt.Errorf("%w: event %d links to %s, want %s",
				ErrChainBroken, i, quoteID(event.GetPrevEventId()), quoteID(prev))
		}
		prev = event.GetEventId()
	}
	return nil
}

// checkOrder walks the accepted order.
//
// A kind it cannot place makes every state after that event a guess, so it
// stops walking the order there and holds the undetermined reading. It does not
// stop reading the sequence: the defects that hold whatever that kind turns out
// to mean are still reported, and reported as definite. Returning at the first
// unplaceable kind instead would hand anyone able to add a line to an evidence
// file a way to turn "this trail is broken" into "this reader cannot tell".
func checkOrder(events []*controlv1.Event) error {
	state := chainStart
	proposals := 0
	startedAs := "" // the execution ACTION_STARTED named, once the walk has taken it
	var unplaceable error

	for i, event := range events {
		kind := event.GetKind()
		if kind == controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED {
			proposals++
		}
		if err := definiteDefect(i, event, proposals); err != nil {
			return err
		}
		if unplaceable != nil {
			continue
		}
		next, result := state.step(kind)
		switch result {
		case stepAllowed:
			ran, err := execution(i, event, startedAs)
			if err != nil {
				return err
			}
			state, startedAs = next, ran
		case stepUnplaceable:
			unplaceable = fmt.Errorf("%w: event %d has kind %d, which this version cannot place",
				ErrChainIndeterminate, i, int32(kind))
		case stepRefused:
			return fmt.Errorf("%w: event %d is %s, which cannot follow %s",
				ErrChainBroken, i, kind, state)
		}
	}

	if unplaceable != nil {
		return unplaceable
	}
	if proposals == 0 {
		// Reached only when every kind was placed, because a later version may
		// well propose an action with a kind this one has never heard of.
		return fmt.Errorf("%w: nothing proposes an action, so the sequence accounts for no request",
			ErrChainBroken)
	}
	return nil
}

// definiteDefect reports the defects that do not depend on where the walk has
// got to, so they are still reported after a kind this version could not place.
// All three hold under any order a later version declares: an event that names
// no kind says nothing about itself wherever it sits; one request_id is one
// proposed action, so a second proposal is two requests in one file rather than
// a step in either; and a record that something ran which does not say what ran
// cannot be joined to whatever ran it.
func definiteDefect(i int, event *controlv1.Event, proposals int) error {
	kind := event.GetKind()
	switch {
	case kind == controlv1.EventKind_EVENT_KIND_UNSPECIFIED:
		// Declared, and it says nothing about the event carrying it. That is a
		// defect in the record rather than a kind from a later version.
		return fmt.Errorf("%w: event %d has no kind", ErrChainBroken, i)
	case kind == controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED && proposals > 1:
		return fmt.Errorf("%w: event %d proposes an action this sequence already proposed",
			ErrChainBroken, i)
	case namesExecution(kind) && event.GetExecutionId() == "":
		return fmt.Errorf("%w: event %d is %s and names no execution", ErrChainBroken, i, kind)
	}
	return nil
}

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
	// see ErrChainIndeterminate.
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
