// Chain tests. They build their sequences through the Builder rather than by
// filling Event structs, so the links under test are the ones the package
// actually writes, and a change to how the Builder links would fail here too.
package evidence_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// Short names for the tables. A kind is forty characters written out and a
// sequence of five does not fit on a line.
const (
	proposed  = controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED
	decided   = controlv1.EventKind_EVENT_KIND_POLICY_DECIDED
	requested = controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED
	answered  = controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED
	expired   = controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED
	started   = controlv1.EventKind_EVENT_KIND_ACTION_STARTED
	completed = controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED
	failed    = controlv1.EventKind_EVENT_KIND_ACTION_FAILED
	blocked   = controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED
	found     = controlv1.EventKind_EVENT_KIND_FINDING_RAISED
	reloaded  = controlv1.EventKind_EVENT_KIND_POLICY_RELOADED

	unspecified = controlv1.EventKind_EVENT_KIND_UNSPECIFIED

	// Not declared by the contract. They stand for kinds a later minor version
	// adds, which this version has to report as undetermined rather than wrong.
	// Two of them, because which one a refusal names is part of what is tested.
	fromTheFuture     = controlv1.EventKind(99)
	alsoFromTheFuture = controlv1.EventKind(100)
)

// trailBuilder is this file's own, rather than event_test.go's mustBuilder,
// because a chain test needs Builders for two different request ids and the
// event tests never do.
func trailBuilder(t *testing.T, requestID, idPrefix string) *evidence.Builder {
	t.Helper()
	ids := evidence.IDs{
		RequestID: requestID,
		RunID:     "run-1",
		ProjectID: "proj-1",
		TenantID:  "tenant-1",
	}
	clock := func() time.Time { return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC) }
	number := 0
	newID := func() string {
		number++
		return idPrefix + "-" + strconv.Itoa(number)
	}
	b, err := evidence.NewBuilder(ids, controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, clock, newID)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	return b
}

// declaredKinds is the eleven the contract declares, in the order the tables
// read. A function rather than a variable: nothing in this package holds
// mutable state at package level, tests included.
func declaredKinds() []controlv1.EventKind {
	return []controlv1.EventKind{
		proposed, decided, requested, answered, expired,
		started, completed, failed, blocked, found, reloaded,
	}
}

// appendKind drives the Builder to produce one event of the given kind and
// fails the test when no constructor produces it.
func appendKind(t *testing.T, b *evidence.Builder, kind controlv1.EventKind) *controlv1.Event {
	t.Helper()
	event := buildKind(b, kind)
	if event == nil {
		t.Fatalf("no constructor produces kind %s; the table needs one", kind)
	}
	return event
}

// The execution every trail in these tests runs as.
const chainExecution = "exec-1"

// buildKind returns nil for a kind no constructor produces. Separate from
// appendKind because the property test drives it from a *rapid.T, which is not
// a testing.TB and cannot be one.
//
// Payload arguments are nil throughout: ValidateChain reads no payload, and a
// nil one is the path a caller takes when it has nothing to record. The
// results are no exception, because ACTION_COMPLETED and ACTION_FAILED name
// the execution the Builder's Started was handed, whatever result they carry.
func buildKind(b *evidence.Builder, kind controlv1.EventKind) *controlv1.Event {
	switch kind {
	case proposed:
		return b.Proposed(nil)
	case decided:
		return b.Decided(nil)
	case requested:
		return b.ApprovalRequested(nil)
	case answered:
		return b.ApprovalDecided(nil)
	case expired:
		return b.ApprovalExpired(nil)
	case started:
		return b.Started(chainExecution)
	case completed:
		return b.Completed(nil)
	case failed:
		return b.Failed(nil)
	case blocked:
		return b.Blocked(nil)
	case found:
		return b.FindingRaised(nil)
	case reloaded:
		return b.PolicyReloaded(nil)
	default:
		return nil
	}
}

// trail builds one linked sequence for request "req-1".
func trail(t *testing.T, kinds ...controlv1.EventKind) []*controlv1.Event {
	t.Helper()
	b := trailBuilder(t, "req-1", "evt")
	events := make([]*controlv1.Event, 0, len(kinds))
	for _, kind := range kinds {
		events = append(events, appendKind(t, b, kind))
	}
	return events
}

// trailOfKinds builds one linked sequence and then stamps the kinds over it, so
// a table can name a kind no constructor produces. The ids, the links and the
// request id are the Builder's own; only the kind is the test's, together with
// the execution id a kind that records something running has to carry. The
// payload each event carries is the reload one and does not match its kind,
// which is exactly the input ValidateChain claims not to read.
func trailOfKinds(t *testing.T, kinds ...controlv1.EventKind) []*controlv1.Event {
	t.Helper()
	b := trailBuilder(t, "req-1", "evt")
	events := make([]*controlv1.Event, 0, len(kinds))
	for _, kind := range kinds {
		event := b.PolicyReloaded(nil)
		event.Kind = kind
		if kind == started || kind == completed || kind == failed {
			event.ExecutionId = chainExecution
		}
		events = append(events, event)
	}
	return events
}

func TestValidateChainAcceptsTheOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kinds []controlv1.EventKind
	}{
		{"ran to completion", []controlv1.EventKind{proposed, decided, started, completed}},
		{"ran and failed", []controlv1.EventKind{proposed, decided, started, failed}},
		{"blocked without running", []controlv1.EventKind{proposed, decided, blocked}},
		{"approved then ran", []controlv1.EventKind{proposed, decided, requested, answered, started, completed}},
		{"approval expired then blocked", []controlv1.EventKind{proposed, decided, requested, expired, blocked}},
		{"rejected then blocked", []controlv1.EventKind{proposed, decided, requested, answered, blocked}},

		// A trail is read while the request is still running, so every prefix
		// of an accepted order is itself accepted.
		{"in flight, proposed only", []controlv1.EventKind{proposed}},
		{"in flight, decided", []controlv1.EventKind{proposed, decided}},
		{"in flight, awaiting an approver", []controlv1.EventKind{proposed, decided, requested}},
		{"in flight, running", []controlv1.EventKind{proposed, decided, started}},

		// An expired window may be opened again. Nothing about asking a second
		// time is a defect; what an expiry may not be followed by is the action
		// starting, which is the case below in the refused table.
		{"approval expired then asked again", []controlv1.EventKind{
			proposed, decided, requested, expired, requested, answered, started, completed}},

		{"reload before anything", []controlv1.EventKind{reloaded, proposed, decided, blocked}},
		{"reload between decision and start", []controlv1.EventKind{proposed, decided, reloaded, started, completed}},
		{"reload after the action closed", []controlv1.EventKind{proposed, decided, blocked, reloaded}},

		{"finding while running", []controlv1.EventKind{proposed, decided, started, found, completed}},
		{"finding after the action closed", []controlv1.EventKind{proposed, decided, blocked, found}},
		{"several findings", []controlv1.EventKind{proposed, found, found, decided, blocked}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := evidence.ValidateChain(trail(t, tc.kinds...)); err != nil {
				t.Errorf("ValidateChain: %v", err)
			}
		})
	}
}

func TestValidateChainRefusesTheOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kinds []controlv1.EventKind
	}{
		{"decided with nothing proposed", []controlv1.EventKind{decided}},
		{"proposed twice", []controlv1.EventKind{proposed, proposed}},
		{"started with no decision", []controlv1.EventKind{proposed, started}},
		{"completed with no start", []controlv1.EventKind{proposed, decided, completed}},
		{"failed with no start", []controlv1.EventKind{proposed, decided, failed}},
		{"blocked with no decision", []controlv1.EventKind{proposed, blocked}},
		{"started while an approval is open", []controlv1.EventKind{proposed, decided, requested, started}},
		{"blocked while an approval is open", []controlv1.EventKind{proposed, decided, requested, blocked}},
		{"answered with nothing requested", []controlv1.EventKind{proposed, decided, answered}},
		{"expired with nothing requested", []controlv1.EventKind{proposed, decided, expired}},
		{"answered twice", []controlv1.EventKind{proposed, decided, requested, answered, answered}},
		{"completed twice", []controlv1.EventKind{proposed, decided, started, completed, completed}},
		{"restarted after completing", []controlv1.EventKind{proposed, decided, started, completed, started}},
		{"blocked after completing", []controlv1.EventKind{proposed, decided, started, completed, blocked}},
		{"finding before anything was proposed", []controlv1.EventKind{found}},
		{"finding ahead of the proposal", []controlv1.EventKind{found, proposed, decided, blocked}},

		// An approval window that closed with nobody in it is not permission.
		{"started after an approval expired", []controlv1.EventKind{
			proposed, decided, requested, expired, started}},
		{"started after a second window expired", []controlv1.EventKind{
			proposed, decided, requested, expired, requested, expired, started}},

		// A trail is an account of one request, so it holds the request's own
		// proposal. Reloads are caused by an operator and account for nothing.
		{"one reload and nothing else", []controlv1.EventKind{reloaded}},
		{"reloads alone", []controlv1.EventKind{reloaded, reloaded}},
		{"a finding and a reload, with nothing proposed", []controlv1.EventKind{reloaded, found}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := evidence.ValidateChain(trail(t, tc.kinds...))
			if !errors.Is(err, evidence.ErrChainBroken) {
				t.Errorf("ValidateChain: got %v, want ErrChainBroken", err)
			}
			// An order this version knows and refuses is a defect, never an
			// undetermined reading.
			if errors.Is(err, evidence.ErrChainIndeterminate) {
				t.Errorf("ValidateChain: got %v, want a definite refusal", err)
			}
		})
	}
}

// An empty slice is the shape a dropped read has. Reporting it as well formed
// would be a pass over nothing.
func TestValidateChainRefusesAnEmptySequence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []*controlv1.Event
	}{
		{"nil slice", nil},
		{"empty slice", []*controlv1.Event{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := evidence.ValidateChain(tc.events); !errors.Is(err, evidence.ErrChainBroken) {
				t.Errorf("ValidateChain: got %v, want ErrChainBroken", err)
			}
		})
	}
}

func TestValidateChainRefusesBrokenLinks(t *testing.T) {
	valid := []controlv1.EventKind{proposed, decided, started, completed}

	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, events []*controlv1.Event) []*controlv1.Event
	}{
		{"the head claims a predecessor", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[0].PrevEventId = "evt-0"
			return e
		}},
		{"a link points somewhere else", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[2].PrevEventId = "evt-9"
			return e
		}},
		{"a link is missing", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[2].PrevEventId = ""
			return e
		}},
		{"an event has no id", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[1].EventId = ""
			return e
		}},
		// The two cases the link check cannot reach on its own: a lone event,
		// and an empty id whose successor points at the same emptiness. Without
		// the id check both of these read as an intact trail.
		{"the only event has no id", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[0].EventId = ""
			return e[:1]
		}},
		{"an event has no id and the link still joins", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[1].EventId = ""
			e[2].PrevEventId = ""
			return e
		}},
		{"an id repeats, so an event is its own predecessor", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			// What a generator that returns one value produces. Every link
			// joins, so nothing but the repeat itself reports it.
			for _, event := range e {
				event.EventId = "same"
				event.PrevEventId = "same"
			}
			e[0].PrevEventId = ""
			return e
		}},
		{"the first event has no request id", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[0].RequestId = ""
			return e
		}},
		{"an event belongs to another request", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[2].RequestId = "req-2"
			return e
		}},
		{"an event is nil", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			e[2] = nil
			return e
		}},
		{"two chains concatenated", func(t *testing.T, e []*controlv1.Event) []*controlv1.Event {
			t.Helper()
			second := trailBuilder(t, "req-2", "other")
			return append(e, appendKind(t, second, proposed))
		}},
		{"the same chain twice", func(_ *testing.T, e []*controlv1.Event) []*controlv1.Event {
			return append(e, e...)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := trail(t, valid...)
			// Control: the sequence has to be valid before it is broken, or
			// the assertion below would pass on something else.
			if err := evidence.ValidateChain(events); err != nil {
				t.Fatalf("the unbroken sequence is already refused: %v", err)
			}
			if err := evidence.ValidateChain(tc.damage(t, events)); !errors.Is(err, evidence.ErrChainBroken) {
				t.Errorf("ValidateChain: got %v, want ErrChainBroken", err)
			}
		})
	}
}

// An undeclared kind is what a trail written by a later minor version looks
// like. event.proto makes that INDETERMINATE to a reader rather than an error,
// so this version may neither pass the sequence nor blame the producer.
func TestValidateChainIsIndeterminateForAKindItCannotPlace(t *testing.T) {
	events := trail(t, proposed, decided, blocked)
	events[1].Kind = fromTheFuture

	err := evidence.ValidateChain(events)
	if !errors.Is(err, evidence.ErrChainIndeterminate) {
		t.Fatalf("ValidateChain: got %v, want ErrChainIndeterminate", err)
	}
	if errors.Is(err, evidence.ErrChainBroken) {
		t.Errorf("ValidateChain: got %v, which also reads as a defect in the trail", err)
	}
}

// A kind this version cannot place makes the state after it a guess. It must
// not make a defect that holds whatever that state is unreportable: one line
// with an undeclared kind number is what anyone able to write to an evidence
// file can add, and to an auditor "this trail is broken" and "this reader
// cannot tell" are different statements.
//
// The first two cases are the boundary itself, recorded in both directions.
func TestValidateChainDoesNotLaunderADefectBehindAKindItCannotPlace(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kinds    []controlv1.EventKind
		definite bool   // whether the answer may be ErrChainBroken
		names    string // the event the refusal has to name
	}{
		{"a step that depends on the lost state stays undetermined",
			[]controlv1.EventKind{proposed, decided, fromTheFuture, completed}, false, "event 2"},
		{"a second proposal is a defect whatever the state is",
			[]controlv1.EventKind{proposed, decided, fromTheFuture, proposed}, true, "event 3"},
		{"an unspecified kind is a defect whatever the state is",
			[]controlv1.EventKind{proposed, decided, fromTheFuture, unspecified}, true, "event 3"},
		{"the undeclared kind ahead of everything",
			[]controlv1.EventKind{fromTheFuture, proposed, decided, proposed}, true, "event 3"},
		{"a defect ahead of the undeclared kind is still definite",
			[]controlv1.EventKind{proposed, proposed, fromTheFuture}, true, "event 1"},
		{"the first kind that cannot be placed is the one reported",
			[]controlv1.EventKind{proposed, fromTheFuture, alsoFromTheFuture, completed}, false, "event 1"},

		// A later version may well propose an action with a kind this one has
		// never heard of, so "nothing here proposes anything" is not a reading
		// this version is entitled to when it could not place the kinds.
		{"a sequence none of whose kinds can be placed",
			[]controlv1.EventKind{fromTheFuture, alsoFromTheFuture}, false, "event 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, other := evidence.ErrChainIndeterminate, evidence.ErrChainBroken
			if tc.definite {
				want, other = other, want
			}
			err := evidence.ValidateChain(trailOfKinds(t, tc.kinds...))
			if !errors.Is(err, want) {
				t.Fatalf("ValidateChain: got %v, want %v", err, want)
			}
			if errors.Is(err, other) {
				t.Errorf("ValidateChain: got %v, which also reads as %v", err, other)
			}
			// Which event is blamed is the whole point of walking on rather
			// than stopping, so the index is asserted and not only the class.
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("ValidateChain: got %v, want it to name %s", err, tc.names)
			}
		})
	}
}

// UNSPECIFIED is declared, and it says nothing about the event carrying it.
// That is a defect in the record, not a kind from a later version.
func TestValidateChainRefusesAnUnspecifiedKind(t *testing.T) {
	events := trail(t, proposed, decided, blocked)
	events[1].Kind = controlv1.EventKind_EVENT_KIND_UNSPECIFIED

	err := evidence.ValidateChain(events)
	if !errors.Is(err, evidence.ErrChainBroken) {
		t.Errorf("ValidateChain: got %v, want ErrChainBroken", err)
	}
	if errors.Is(err, evidence.ErrChainIndeterminate) {
		t.Errorf("ValidateChain: got %v, want a definite refusal", err)
	}
}

// A definite failure outranks an undetermined one, the same way DENY outranks
// INDETERMINATE in the contract's precedence. A caller that saw only
// "indeterminate" here would retry or wait for a newer reader instead of
// reporting the gap it actually has.
func TestValidateChainReportsABrokenLinkAheadOfAnUnknownKind(t *testing.T) {
	events := trail(t, proposed, decided, blocked)
	events[1].Kind = fromTheFuture
	events[2].PrevEventId = "evt-9"

	err := evidence.ValidateChain(events)
	if !errors.Is(err, evidence.ErrChainBroken) {
		t.Fatalf("ValidateChain: got %v, want ErrChainBroken", err)
	}
	if errors.Is(err, evidence.ErrChainIndeterminate) {
		t.Errorf("ValidateChain: got %v, want the link reported on its own", err)
	}
}

// The order this version knows has to cover every kind the contract declares.
// A twelfth kind added later reaches this test as an undetermined reading,
// which is the signal to place it in the order deliberately rather than to
// discover it in production.
func TestValidateChainPlacesEveryDeclaredKind(t *testing.T) {
	for number, name := range controlv1.EventKind_name {
		kind := controlv1.EventKind(number)
		if kind == controlv1.EventKind_EVENT_KIND_UNSPECIFIED {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// After ACTION_PROPOSED, because that is the state from which the
			// most kinds are legal. Whether this particular pair is accepted or
			// refused is the order's business; what matters here is that the
			// kind was placed at all.
			b := trailBuilder(t, "req-1", "evt")
			events := []*controlv1.Event{appendKind(t, b, proposed), appendKind(t, b, kind)}

			if err := evidence.ValidateChain(events); errors.Is(err, evidence.ErrChainIndeterminate) {
				t.Errorf("ValidateChain cannot place %s: %v", name, err)
			}
		})
	}
}

// The documented scope, pinned from the other side: the validator reads kinds,
// not payloads. An approval carries its outcome in its payload, so a trail in
// which the approver said no and the action ran anyway passes here. This is a
// recorded limitation and not a wish: whoever reports a chain as well formed
// may not report it as evidence that the approval gate held.
func TestValidateChainDoesNotReadPayloads(t *testing.T) {
	b := trailBuilder(t, "req-1", "evt")
	events := []*controlv1.Event{
		b.Proposed(nil),
		b.Decided(&controlv1.Decision{Verdict: controlv1.Verdict_VERDICT_REQUIRE_APPROVAL}),
		b.ApprovalRequested(&controlv1.Approval{ApprovalId: "app-1"}),
		b.ApprovalDecided(&controlv1.Approval{
			ApprovalId: "app-1",
			State:      controlv1.ApprovalState_APPROVAL_STATE_REJECTED,
		}),
		b.Started(chainExecution),
		b.Completed(nil),
	}

	if err := evidence.ValidateChain(events); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
}

// The documented scope, pinned: the validator reads links and kinds, not
// clocks. A trail whose timestamps go backwards is a question for whoever
// reads the times, and making it a chain failure here would turn every clock
// step onto a chain refusal.
func TestValidateChainDoesNotOrderByTime(t *testing.T) {
	events := trail(t, proposed, decided, blocked)
	events[0].OccurredAt = timestamppb.New(time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC))
	events[2].OccurredAt = timestamppb.New(time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC))

	if err := evidence.ValidateChain(events); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
}
