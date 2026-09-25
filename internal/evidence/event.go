// Package evidence builds the records that make a decision reviewable months
// later by someone who was not there.
//
// Nothing here reaches a store, a network or a wall clock: a Builder is handed
// its clock and its id generator, so a sequence of events is reproducible from
// its inputs. Spools and exporters live outside this package.
//
// Payloads arrive already redacted. This package never inspects a payload,
// never rewrites one and never lifts text out of one into an event field; which
// redaction profile ran is recorded by the producer inside the message itself
// (ADR-0004). The payload is deep-copied into the event, so the record does not
// change when the caller edits or reuses the message it was built from.
//
// A nil payload leaves the oneof arm unset rather than setting the arm to a nil
// message. An arm holding nil marshals as a present but empty message, and an
// empty Decision or Approval reads as UNSPECIFIED, which the contract defines
// as "nothing was decided". A gap has to look like a gap.
//
// Event.enforcement_mode is the enforcing plane's own mode, handed to
// NewBuilder and stamped on every event. A Decision carries an
// enforcement_mode of its own, which is producer-supplied and can come from an
// external decision point; it stays inside the payload. The two are never
// merged, which is what makes each one readable: the top-level field is always
// this plane's statement, the payload's is always the producer's.
// Event.execution_id is held the same way. It is the id handed to Started,
// stamped again on the event that closes the action, and an ActionResult's own
// execution_id stays inside the result.
//
// prev_event_id links an event to the one before it on the same request_id. It
// is an ordering link and nothing more: it shows a gap, it does not show that a
// record was not altered. See ADR-0004.
package evidence

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// SchemaVersion is the value every v1 event carries. The frozen contract sets
// it; this constant only spells it once.
const SchemaVersion = "1.0"

// ErrInvalidInput reports that NewBuilder was handed something it cannot build
// a complete event from: an identifier that is absent or unusable, a mode that
// says nothing, or a nil clock or generator. There is no Builder that cannot
// stamp what every record has to carry.
var ErrInvalidInput = errors.New("evidence: invalid builder input")

// IDs are the correlation identifiers that invariant 10 of AGENTS.md requires
// on every proposed action, decision, approval and result. NewBuilder refuses a
// missing RequestID, ProjectID or TenantID, so an event without them cannot be
// built here. That is a statement about construction and not about the record:
// the Event handed back is an ordinary message, and a caller that clears a
// field on its own copy is something no constructor can prevent.
//
// RunID is the deliberate exception and may be empty. A run groups several
// requests and not every action belongs to one, so an action taken outside a
// run is a fact about it rather than a missing identifier. When it is present
// it has to be usable like the rest.
//
// Usable means what contract.CheckIdentifier accepts, the one rule the
// contract holds every identifier of an envelope to (ADR-0011), and no longer
// than contract.MaxStringBytes, the contract's bound on a string. The event's
// request_id and the envelope's are one value, so a rule of the Builder's own
// would let a request the contract accepts have no trail, or let a trail carry
// an id no accepted envelope could hold. request_id is the scope of the
// whole chain, so " req-1 " and "req-1" would be two trails for one request,
// and a zero width space would be a difference nobody can see. Refused rather
// than trimmed: the id in the record has to be the id the producer sent, or
// the event's own request_id stops matching the one inside the payload it
// carries.
type IDs struct {
	RequestID, RunID, ProjectID, TenantID string
}

// Builder makes the events of one request's evidence trail.
//
// A Builder is not safe for concurrent use. It carries the id of the last event
// it produced, and prev_event_id is a sequence, so two goroutines sharing one
// Builder would interleave into an order neither of them chose. A lock would
// remove the data race and leave that ordering problem, so there is none. One
// Builder belongs to one request_id; a caller that hands the trail to another
// goroutine establishes the happens-before edge itself.
//
// NewBuilder is the only way to make a usable one. A composite literal from
// another package compiles, because an empty literal is always legal, and it
// panics on its first constructor call with no clock to read. That is a wiring
// fault: no zero value this package could give it would do anything but put
// evidence with no time and no id into a trail.
type Builder struct {
	ids   IDs
	mode  controlv1.EnforcementMode
	clock func() time.Time
	newID func() string
	// The event_id of the last event this Builder made; empty before the first.
	prev string
	// The execution_id the last Started was handed; empty before any. Completed
	// and Failed stamp it, so the event that closes an action names the one
	// this plane recorded starting.
	started string
}

// NewBuilder returns a Builder that stamps ids and mode on every event, times
// it with clock and names it with newID.
//
// mode is the enforcing plane's own mode as it stood when the Builder was made.
// A Builder belongs to one request, so a mode that changes in the middle of one
// (an emergency pause is planned) is recorded by the next request's Builder
// rather than this one, and the window is a single request.
//
// It fails with ErrInvalidInput, naming each input and what is wrong with it,
// when an identifier breaks a rule on IDs, when mode is
// ENFORCEMENT_MODE_UNSPECIFIED or a number the v1 contract does not declare, or
// when clock or newID is nil. Such a mode is refused with the identifiers:
// without a mode a reader can name, it cannot tell an enforced DENY from an
// observed one, and ValidateChain reads every event stamped with an undeclared
// one as undetermined. The Builder is nil then, and a caller that builds anyway
// panics inside this package, which is the trade for not writing evidence
// nobody can join to a request.
//
// clock and newID are trusted after that, and a reader of the trail is what
// checks them. newID has to return a unique, non-empty id no longer than
// contract.MaxStringBytes: an empty one makes every event look like the head
// of a trail, because prev_event_id is empty there too, a repeating one makes
// an event its own predecessor, and ValidateChain refuses a longer one. clock
// has to return a time protobuf can represent, because a year outside 1 to
// 9999 builds a record the binary encoding accepts and the JSON encoding
// refuses.
func NewBuilder(
	ids IDs, mode controlv1.EnforcementMode, clock func() time.Time, newID func() string,
) (*Builder, error) {
	problems := make([]string, 0, 7)
	problems = idProblem(problems, "IDs.RequestID", ids.RequestID, required)
	problems = idProblem(problems, "IDs.ProjectID", ids.ProjectID, required)
	problems = idProblem(problems, "IDs.TenantID", ids.TenantID, required)
	problems = idProblem(problems, "IDs.RunID", ids.RunID, optional)
	switch _, declared := controlv1.EnforcementMode_name[int32(mode)]; {
	case !declared:
		problems = append(problems, fmt.Sprintf("mode %d is not declared by the v1 contract", int32(mode)))
	case mode == controlv1.EnforcementMode_ENFORCEMENT_MODE_UNSPECIFIED:
		problems = append(problems, "mode is unspecified")
	}
	if clock == nil {
		problems = append(problems, "clock is nil")
	}
	if newID == nil {
		problems = append(problems, "newID is nil")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrInvalidInput, strings.Join(problems, ", "))
	}
	return &Builder{ids: ids, mode: mode, clock: clock, newID: newID}, nil
}

// Whether an identifier may be absent; named because idProblem(..., true) says
// nothing at a call site.
const (
	required = false
	optional = true
)

// idProblem appends what is wrong with one identifier, if anything, by the
// rules stated on IDs. The length goes first, so a very long value is refused
// without being read through. Neither refusal repeats the value, which is the
// caller's: the contract's names the class and the code point by number.
func idProblem(problems []string, name, value string, mayBeEmpty bool) []string {
	switch {
	case value == "":
		if !mayBeEmpty {
			return append(problems, name+" is empty")
		}
	case len(value) > contract.MaxStringBytes:
		return append(problems, fmt.Sprintf("%s is %d bytes, over %d", name, len(value), contract.MaxStringBytes))
	default:
		if err := contract.CheckIdentifier(value); err != nil {
			return append(problems, fmt.Sprintf("%s: %v", name, err))
		}
	}
	return problems
}

// next stamps the fields every event carries and advances the ordering link.
func (b *Builder) next(kind controlv1.EventKind) *controlv1.Event {
	id := b.newID()
	event := &controlv1.Event{
		EventId:         id,
		Kind:            kind,
		RequestId:       b.ids.RequestID,
		RunId:           b.ids.RunID,
		ProjectId:       b.ids.ProjectID,
		TenantId:        b.ids.TenantID,
		OccurredAt:      timestamppb.New(b.clock()),
		SchemaVersion:   SchemaVersion,
		EnforcementMode: b.mode,
		PrevEventId:     b.prev,
	}
	b.prev = id
	return event
}

// cloned deep-copies a payload on its way into an event: ADR-0004 has events
// waiting in a spool and serialized well after the call, and one Decision
// passed to both Decided and Blocked would otherwise leave two events sharing
// one mutable message.
//
// Generic so the assertion is on the concrete type proto.Clone was handed and
// is documented to preserve.
func cloned[M proto.Message](m M) M { return proto.Clone(m).(M) }

// Proposed records an action a caller intends to take, before anything has
// decided on it.
func (b *Builder) Proposed(env *controlv1.ActionEnvelope) *controlv1.Event {
	event := b.next(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED)
	if env != nil {
		event.Payload = &controlv1.Event_Proposed{Proposed: cloned(env)}
	}
	return event
}

// Decided records the verdict a policy decision point returned.
func (b *Builder) Decided(d *controlv1.Decision) *controlv1.Event {
	return b.decisionEvent(controlv1.EventKind_EVENT_KIND_POLICY_DECIDED, d)
}

// ApprovalRequested records that a decision needed a person and that the
// request went out.
func (b *Builder) ApprovalRequested(a *controlv1.Approval) *controlv1.Event {
	return b.approvalEvent(controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED, a)
}

// ApprovalDecided records that a person answered, whichever way. An approval
// window that closed with nobody answering is ApprovalExpired.
func (b *Builder) ApprovalDecided(a *controlv1.Approval) *controlv1.Event {
	return b.approvalEvent(controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED, a)
}

// Started records that the action began to run. executionID names that run,
// and the Builder stamps it again on the Completed or Failed event that closes
// it. An empty one builds a record saying something started without saying
// what, and one longer than contract.MaxStringBytes a record outside what the
// line bound is sized for. This constructor has no way to refuse either, so
// ValidateChain is what rejects them.
func (b *Builder) Started(executionID string) *controlv1.Event {
	event := b.next(controlv1.EventKind_EVENT_KIND_ACTION_STARTED)
	event.ExecutionId = executionID
	b.started = executionID
	return event
}

// Completed records that the action ran to an end. The caller chooses this
// constructor or Failed; neither reads the result's status to pick a kind.
func (b *Builder) Completed(r *controlv1.ActionResult) *controlv1.Event {
	return b.resultEvent(controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, r)
}

// Failed records that the action did not complete.
func (b *Builder) Failed(r *controlv1.ActionResult) *controlv1.Event {
	return b.resultEvent(controlv1.EventKind_EVENT_KIND_ACTION_FAILED, r)
}

// Blocked records that the action was stopped, and carries the decision that
// stopped it so the trail names what was enforced rather than only that
// something was.
func (b *Builder) Blocked(d *controlv1.Decision) *controlv1.Event {
	return b.decisionEvent(controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED, d)
}

// ApprovalExpired records an approval window that closed with nobody deciding.
// It is its own kind because "nobody answered" and "somebody said no" are
// different facts about an operator's controls.
func (b *Builder) ApprovalExpired(a *controlv1.Approval) *controlv1.Event {
	return b.approvalEvent(controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED, a)
}

// FindingRaised records what a detector reported after the fact. A finding
// annotates a trail that is already written; it never grants or denies
// authority.
func (b *Builder) FindingRaised(f *controlv1.Finding) *controlv1.Event {
	event := b.next(controlv1.EventKind_EVENT_KIND_FINDING_RAISED)
	if f != nil {
		event.Payload = &controlv1.Event_Finding{Finding: cloned(f)}
	}
	return event
}

// PolicyReloaded records that a new policy bundle was taken into use. The
// reference identifies the bundle; its content never enters the trail.
func (b *Builder) PolicyReloaded(ref *controlv1.PolicyBundleRef) *controlv1.Event {
	event := b.next(controlv1.EventKind_EVENT_KIND_POLICY_RELOADED)
	if ref != nil {
		event.Payload = &controlv1.Event_Policy{Policy: cloned(ref)}
	}
	return event
}

// The decision's own enforcement_mode is deliberately not copied to the event's
// top-level field; see the package doc for whose statement each one is.
func (b *Builder) decisionEvent(kind controlv1.EventKind, d *controlv1.Decision) *controlv1.Event {
	event := b.next(kind)
	if d != nil {
		event.Payload = &controlv1.Event_Decision{Decision: cloned(d)}
	}
	return event
}

func (b *Builder) approvalEvent(kind controlv1.EventKind, a *controlv1.Approval) *controlv1.Event {
	event := b.next(kind)
	if a != nil {
		event.Payload = &controlv1.Event_Approval{Approval: cloned(a)}
	}
	return event
}

// resultEvent stamps the execution the last Started was handed, and not the
// result's. The result's execution_id is the producer's statement about what
// ran and stays in the payload; the event's is this plane's, as
// enforcement_mode is. A result naming another execution, or no result at all,
// still closes the action this trail started, and a trail allows one start, so
// a retry is a new request rather than a new execution in this one. Before any
// Started there is nothing to stamp, and ValidateChain refuses the event.
func (b *Builder) resultEvent(kind controlv1.EventKind, r *controlv1.ActionResult) *controlv1.Event {
	event := b.next(kind)
	event.ExecutionId = b.started
	if r != nil {
		event.Payload = &controlv1.Event_Result{Result: cloned(r)}
	}
	return event
}
