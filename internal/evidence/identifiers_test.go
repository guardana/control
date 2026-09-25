// Identifier tests, item 6. NewBuilder holds the four identifiers it is given
// to the contract's identifier rule and to its bound on a string. ValidateChain
// holds every identifier it reads to that bound, which covers the two a Builder
// is handed call by call and cannot refuse: event_id from newID, and
// execution_id from Started.
package evidence_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/pkg/contract"
)

// idField is one identifier NewBuilder takes: the name its refusal uses, how
// to set it, and where an event carries it.
type idField struct {
	name string
	set  func(ids *evidence.IDs, value string)
	get  func(e *controlv1.Event) string
}

func idFields() []idField {
	return []idField{
		{"IDs.RequestID", func(ids *evidence.IDs, v string) { ids.RequestID = v }, (*controlv1.Event).GetRequestId},
		{"IDs.ProjectID", func(ids *evidence.IDs, v string) { ids.ProjectID = v }, (*controlv1.Event).GetProjectId},
		{"IDs.TenantID", func(ids *evidence.IDs, v string) { ids.TenantID = v }, (*controlv1.Event).GetTenantId},
		{"IDs.RunID", func(ids *evidence.IDs, v string) { ids.RunID = v }, (*controlv1.Event).GetRunId},
	}
}

// Each identifier NewBuilder takes is bounded at contract.MaxStringBytes, the
// contract's bound on a string and the length the line bound is sized for
// (TestTheLargestProposalTheContractAcceptsFitsOnALine). Exactly the bound is
// accepted and reaches the event whole. One byte more is refused, naming the
// field and not repeating the value. Without the bound the second one builds,
// and every event carries an identifier that no envelope Validate accepts
// could hold.
func TestNewBuilderBoundsEachIdentifierAtMaxStringBytes(t *testing.T) {
	for _, field := range idFields() {
		for _, tc := range []struct {
			length int
			accept bool
		}{
			{contract.MaxStringBytes, true},
			{contract.MaxStringBytes + 1, false},
		} {
			t.Run(fmt.Sprintf("%s/%d", field.name, tc.length), func(t *testing.T) {
				value := strings.Repeat("i", tc.length)
				ids := testIDs()
				field.set(&ids, value)
				b, err := evidence.NewBuilder(ids, modeEnforce, stubClock(), stubIDs("evt"))

				if tc.accept {
					if err != nil {
						t.Fatalf("NewBuilder refused an identifier of exactly the bound: %v", err)
					}
					if got := field.get(b.Started("exec-1")); got != value {
						t.Errorf("the event carries %d bytes of %s, want all %d", len(got), field.name, tc.length)
					}
					return
				}
				if !errors.Is(err, evidence.ErrInvalidInput) {
					t.Fatalf("NewBuilder: got %v, want ErrInvalidInput", err)
				}
				if b != nil {
					t.Error("NewBuilder returned a Builder alongside an error")
				}
				if !strings.Contains(err.Error(), field.name) {
					t.Errorf("the refusal does not name %s: %v", field.name, err)
				}
				if len(err.Error()) > 256 {
					t.Errorf("the refusal is %d bytes long, so it carries the value: %.120q", len(err.Error()), err)
				}
			})
		}
	}
}

// NewBuilder takes its identifier rule from the contract rather than keeping
// one of its own (ADR-0011). The old rule passed the first four
// runes because each is printable. They are three Hangul fillers and a
// combining grapheme joiner, every one a default-ignorable code point that
// renders as nothing. Two spaces other than U+0020 come next, which nobody can
// tell from a space. Each is refused, and the refusal names the class. A
// private-use code point is accepted, as ADR-0011 says; the old rule refused
// it. The runes are built from their numbers, so this file holds none raw.
func TestNewBuilderHoldsIdentifiersToTheContractRule(t *testing.T) {
	for _, tc := range []struct {
		name  string
		r     rune
		class string // what the refusal has to name; "" means accepted
	}{
		{"a Hangul filler", 0x3164, "default-ignorable"},
		{"a Hangul choseong filler", 0x115f, "default-ignorable"},
		{"a combining grapheme joiner", 0x034f, "default-ignorable"},
		{"a halfwidth Hangul filler", 0xffa0, "default-ignorable"},
		{"a no-break space", 0x00a0, "space other than U+0020"},
		{"an ideographic space", 0x3000, "space other than U+0020"},
		{"a private-use code point", 0xe000, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := evidence.NewBuilder(idsWith("req"+string(tc.r)+"1"), modeEnforce, stubClock(), stubIDs("evt"))
			if tc.class == "" {
				if err != nil {
					t.Fatalf("NewBuilder: got %v, want a Builder", err)
				}
				return
			}
			if !errors.Is(err, evidence.ErrInvalidInput) {
				t.Fatalf("NewBuilder: got %v, want ErrInvalidInput", err)
			}
			for _, want := range []string{"IDs.RequestID", tc.class} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// An event's request_id and its envelope's are one value, so NewBuilder and
// contract.Validate have to agree on it. A request id that one accepts
// and the other refuses makes a trail whose events and whose proposal cannot
// both exist. The ids are drawn from runes the two rules could treat
// differently and from lengths either side of the bound.
func TestNewBuilderAndValidateAgreeOnARequestID(t *testing.T) {
	envelope := func(requestID string) *controlv1.ActionEnvelope {
		return &controlv1.ActionEnvelope{
			SchemaVersion: "1.0",
			RequestId:     requestID,
			ProjectId:     "proj-1",
			TenantId:      "tenant-1",
			OccurredAt:    timestamppb.New(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)),
			Principal:     &controlv1.Principal{Id: "user-1"},
			Action:        &controlv1.Action{Name: "files.read", Effect: controlv1.EffectClass_EFFECT_CLASS_READ},
			Resource:      &controlv1.Resource{Type: "file"},
		}
	}
	// Control: Validate accepts the envelope around an ordinary id, so a
	// disagreement below is about the id and nothing else.
	if err := contract.Validate(envelope("req-1")); err != nil {
		t.Fatalf("contract.Validate refuses the envelope around an ordinary id: %v", err)
	}

	runes := []rune{
		'a', 'Z', '0', '-', ' ', '"', 0x09, 0x00, 0x7f, 0x85, 0xa0, 0xad, 0xe9, 0x034f, 0x115f, 0x180e,
		0x200b, 0x200e, 0x2028, 0x202e, 0x2060, 0x2800, 0x3000, 0x3164, 0xe000, 0xfeff, 0xffa0, 0x1f600,
	}
	rapid.Check(t, func(rt *rapid.T) {
		id := rapid.OneOf(
			rapid.StringOf(rapid.SampledFrom(runes)),
			rapid.StringOfN(rapid.Just('r'), contract.MaxStringBytes-1, contract.MaxStringBytes+1, -1),
		).Draw(rt, "request_id")

		ids := evidence.IDs{RequestID: id, ProjectID: "proj-1", TenantID: "tenant-1"}
		_, builderErr := evidence.NewBuilder(ids, modeEnforce, stubClock(), stubIDs("evt"))
		validateErr := contract.Validate(envelope(id))
		if (builderErr == nil) != (validateErr == nil) {
			rt.Fatalf("request_id %.80q (%d bytes): NewBuilder says %v, Validate says %v",
				id, len(id), builderErr, validateErr)
		}
	})
}

// ValidateChain holds every identifier it reads to contract.MaxStringBytes:
// the scope, the event ids and their links, and the execution. A Builder
// bounds the four it is given. event_id comes from newID and execution_id from
// Started, call by call, and neither can be refused where it is made, so a
// longer one is refused here, where the documentation of both already sends
// the check. A trail that carried one would sit outside what the line bound is
// sized for. Exactly the bound is accepted, and one byte more is refused,
// naming the event and the field.
func TestValidateChainBoundsEveryIdentifierItReads(t *testing.T) {
	for _, tc := range []struct {
		field string // as the refusal names it
		trail func(t *testing.T, n int) []*controlv1.Event
		names string // the event the refusal has to name
	}{
		{"request_id", onEveryEvent(func(e *controlv1.Event, v string) { e.RequestId = v }), "event 0"},
		{"project_id", onEveryEvent(func(e *controlv1.Event, v string) { e.ProjectId = v }), "event 0"},
		{"tenant_id", onEveryEvent(func(e *controlv1.Event, v string) { e.TenantId = v }), "event 0"},
		{"event_id", eventIDsOf, "event 0"},
		{"execution_id", executionOf, "event 2"},
	} {
		for _, bound := range []struct {
			length int
			accept bool
		}{
			{contract.MaxStringBytes, true},
			{contract.MaxStringBytes + 1, false},
		} {
			t.Run(fmt.Sprintf("%s/%d", tc.field, bound.length), func(t *testing.T) {
				err := evidence.ValidateChain(tc.trail(t, bound.length))
				if bound.accept {
					if err != nil {
						t.Fatalf("ValidateChain refused an identifier of exactly the bound: %.200v", err)
					}
					return
				}
				checkDefinite(t, err, tc.names, tc.field)
			})
		}
	}

	// prev_event_id is bounded as the other identifiers are, and not only
	// through the link it fails to make: the link refusal would name the
	// event without naming the field.
	t.Run("prev_event_id", func(t *testing.T) {
		events := eventIDsOf(t, contract.MaxStringBytes)
		events[1].PrevEventId = strings.Repeat("p", contract.MaxStringBytes+1)
		checkDefinite(t, evidence.ValidateChain(events), "event 1", "prev_event_id")
	})

	// A definite defect wherever it sits, so a kind this version cannot place
	// does not turn it into an undetermined reading.
	t.Run("after a kind this version cannot place", func(t *testing.T) {
		events := trailOfKinds(t, proposed, decided, fromTheFuture, started, completed)
		long := strings.Repeat("x", contract.MaxStringBytes+1)
		events[3].ExecutionId, events[4].ExecutionId = long, long
		checkDefinite(t, evidence.ValidateChain(events), "event 3", "execution_id")
	})

	// The refusal states the length and does not quote the value, so a very
	// long identifier makes a short refusal.
	t.Run("a very long identifier makes a short refusal", func(t *testing.T) {
		err := evidence.ValidateChain(executionOf(t, 200_000))
		checkDefinite(t, err, "event 2", "execution_id")
		if err != nil && len(err.Error()) > 256 {
			t.Errorf("the refusal is %d bytes long: %.120q...", len(err.Error()), err)
		}
	})
}

// onEveryEvent makes a trail whose every event carries one identifier n bytes
// long.
func onEveryEvent(set func(*controlv1.Event, string)) func(t *testing.T, n int) []*controlv1.Event {
	return func(t *testing.T, n int) []*controlv1.Event {
		t.Helper()
		events := trail(t, ranToCompletion()...)
		value := strings.Repeat("s", n)
		for _, event := range events {
			set(event, value)
		}
		return events
	}
}

// eventIDsOf makes a trail the Builder wrote with a generator whose every id is
// n bytes long, so the links are the Builder's own.
func eventIDsOf(t *testing.T, n int) []*controlv1.Event {
	t.Helper()
	number := 0
	newID := func() string {
		number++
		prefix := fmt.Sprintf("evt-%d-", number)
		return prefix + strings.Repeat("e", n-len(prefix))
	}
	ids := evidence.IDs{RequestID: "req-1", ProjectID: "proj-1", TenantID: "tenant-1"}
	b, err := evidence.NewBuilder(ids, modeEnforce, stubClock(), newID)
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	events := make([]*controlv1.Event, 0, len(ranToCompletion()))
	for _, kind := range ranToCompletion() {
		events = append(events, appendKind(t, b, kind))
	}
	return events
}

// executionOf makes a trail whose action ran as an execution n bytes long,
// which the Builder stamps on the start and again on the close.
func executionOf(t *testing.T, n int) []*controlv1.Event {
	t.Helper()
	b := trailBuilder(t, "req-1", "evt")
	return []*controlv1.Event{b.Proposed(nil), b.Decided(nil), b.Started(strings.Repeat("x", n)), b.Completed(nil)}
}

// checkDefinite fails unless err is a definite refusal that names every one of
// names.
func checkDefinite(t *testing.T, err error, names ...string) {
	t.Helper()
	if !errors.Is(err, evidence.ErrChainBroken) {
		t.Fatalf("ValidateChain: got %.200v, want ErrChainBroken", err)
	}
	if errors.Is(err, evidence.ErrChainIndeterminate) {
		t.Errorf("ValidateChain: got %.200v, want a definite refusal", err)
	}
	for _, want := range names {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidateChain: got %.200v, want it to name %q", err, want)
		}
	}
}
