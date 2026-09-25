// The tests read the package through its exported surface only, which is all
// a caller has. Every stub here is fixed: no test in this file reads a wall
// clock or draws a random value, so a failure is the same failure on any
// machine on any day.
//
// The identifiers the Builder is given and the identifiers inside the payload
// fixtures are deliberately different strings. They are the same value in a
// real request, and a test that used one value could not tell an event stamped
// from IDs apart from one that copied a producer-supplied field, which is the
// distinction invariant 10 rests on.
package evidence_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
)

// A string that exists only inside payload fixtures. Invariant 9: no
// constructor may lift content out of a payload into a field of the event.
const contentMarker = "content-marker-do-not-copy-4f21"

// The instant every stub clock starts from. Fixed, because "reproducible" here
// means the same bytes on any machine on any day.
func baseTime() time.Time { return time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC) }

// stubClock hands out times one second apart from baseTime, so a test can tell
// which call produced which timestamp.
func stubClock() func() time.Time {
	calls := 0
	return func() time.Time {
		t := baseTime().Add(time.Duration(calls) * time.Second)
		calls++
		return t
	}
}

// stubIDs numbers ids from one, so an assertion can name the id it expects
// instead of comparing an event against itself.
func stubIDs(prefix string) func() string {
	calls := 0
	return func() string {
		calls++
		return fmt.Sprintf("%s-%d", prefix, calls)
	}
}

func testIDs() evidence.IDs {
	return evidence.IDs{
		RequestID: "req-from-builder",
		RunID:     "run-from-builder",
		ProjectID: "proj-from-builder",
		TenantID:  "tenant-from-builder",
	}
}

// A second complete set, sharing no string with testIDs() and none with the
// payload fixtures. Its reason for existing is stamps() below.
func otherIDs() evidence.IDs {
	return evidence.IDs{
		RequestID: "req-second-builder",
		RunID:     "run-second-builder",
		ProjectID: "proj-second-builder",
		TenantID:  "tenant-second-builder",
	}
}

// Both differ from the mode inside the decision fixture, so a constructor that
// copied the producer's mode up into the event's own field shows up as a wrong
// value rather than as one that happens to match.
const (
	modeEnforce = controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE
	modeApprove = controlv1.EnforcementMode_ENFORCEMENT_MODE_APPROVE
)

// stamp is everything an event carries from its Builder: the four correlation
// identifiers and the enforcing plane's own mode. Tests take one of these
// rather than the values one at a time, so a sixth stamped field is added here
// and not at every call site.
type stamp struct {
	name string
	ids  evidence.IDs
	mode controlv1.EnforcementMode
}

// The two stamps share no value, and TestTheTwoStampsShareNoValue holds that.
// A constant shared between the code under test and the assertion about it can
// never fail: with one mode everywhere a next() that wrote the literal ENFORCE
// survives the suite, and with one set of identifiers everywhere a next() that
// wrote the literal "proj-from-builder" survives it too. Both were measured
// surviving, in round 2 and round 3.
func enforcing() stamp { return stamp{name: "enforce", ids: testIDs(), mode: modeEnforce} }
func approving() stamp { return stamp{name: "approve", ids: otherIDs(), mode: modeApprove} }

func stamps() []stamp { return []stamp{enforcing(), approving()} }

// declaredModes is every mode the contract declares except UNSPECIFIED, which
// NewBuilder refuses. A Builder has no opinion about which of them a plane may
// be in; it stamps what it was given.
func declaredModes() []controlv1.EnforcementMode {
	modes := make([]controlv1.EnforcementMode, 0, len(controlv1.EnforcementMode_name)-1)
	for number := range controlv1.EnforcementMode_name {
		if number != 0 {
			modes = append(modes, controlv1.EnforcementMode(number))
		}
	}
	sort.Slice(modes, func(i, j int) bool { return modes[i] < modes[j] })
	return modes
}

// mustBuilder fails the test instead of returning an error, so a test body
// reads as the sequence of events it is about. Every call names its stamp: a
// default would put those values back in one place and take them out of the
// assertions. enforcing() is the stamp for the tests that are about something
// else, and the matrix in TestEveryConstructorStampsIdentifiers is the only
// place where a stamped value is read from more than one.
func mustBuilder(t *testing.T, s stamp, idPrefix string) *evidence.Builder {
	t.Helper()
	b, err := evidence.NewBuilder(s.ids, s.mode, stubClock(), stubIDs(idPrefix))
	if err != nil {
		t.Fatalf("NewBuilder with complete input: %v", err)
	}
	return b
}

// One row per constructor. The rows drive the table tests below, and
// TestTableCoversEveryConstructorAndKind fails when a twelfth constructor
// appears without one, so a new event kind cannot quietly skip the identifier
// checks.
type constructorCase struct {
	method string // the exported method this row calls, checked by reflection
	kind   controlv1.EventKind
	arm    string // the payload arm that must be set, "" when there is none
	// The message the row passes as its payload, and the same pointer the arm
	// must not hold. nil for a constructor that takes no message.
	payload proto.Message
	// The execution_id the event carries when this row's call is the first a
	// Builder makes: Started's argument, and nothing for every other kind. A
	// kind that closes an action names the execution a Started was handed, and
	// on a fresh Builder no Started has been.
	executionID string
	// Whether the kind closes an action, so that its event names the execution
	// the Builder's last Started was handed, whatever result it carries.
	closes bool
	call   func(b *evidence.Builder) *controlv1.Event
	// The same constructor with nothing to record: a nil payload, or an empty
	// execution id where the constructor takes one.
	callEmpty func(b *evidence.Builder) *controlv1.Event
}

// The execution ids the fixtures use. Two different values, so a constructor
// that took the id from the wrong place names which place it took it from.
const (
	startedExecutionID = "exec-from-caller"
	resultExecutionID  = "exec-from-result"
)

// One fixture per payload type. Their correlation identifiers differ from the
// Builder's, and each carries contentMarker in a content-bearing field, so a
// constructor that copied either one out of a payload is visible.
type payloads struct {
	env      *controlv1.ActionEnvelope
	decision *controlv1.Decision
	approval *controlv1.Approval
	result   *controlv1.ActionResult
	finding  *controlv1.Finding
	bundle   *controlv1.PolicyBundleRef
}

// Fresh messages on every call: the fixtures are used by tests that run in any
// order and by one that mutates them on purpose.
func newPayloads() payloads {
	return payloads{
		env: &controlv1.ActionEnvelope{
			SchemaVersion: evidence.SchemaVersion,
			RequestId:     "req-from-payload",
			ProjectId:     "proj-from-payload",
			TenantId:      "tenant-from-payload",
			Principal:     &controlv1.Principal{Id: "user-1", Attributes: map[string]string{"note": contentMarker}},
			Arguments: &controlv1.Arguments{
				RedactedPreview:  contentMarker,
				RedactionProfile: "profile-default",
			},
			Context: &controlv1.RunContext{RunId: "run-from-payload"},
		},
		decision: &controlv1.Decision{
			DecisionId:  "dec-1",
			RequestId:   "req-from-payload",
			Verdict:     controlv1.Verdict_VERDICT_DENY,
			ReasonCodes: []string{contentMarker},
			// Never a mode any Builder here is made with: this is the producer's
			// statement about the decision, and a constructor that copied it
			// into the event's own enforcement_mode would fail the field check.
			EnforcementMode: controlv1.EnforcementMode_ENFORCEMENT_MODE_OBSERVE,
		},
		approval: &controlv1.Approval{
			ApprovalId: "app-1",
			RequestId:  "req-from-payload",
			State:      controlv1.ApprovalState_APPROVAL_STATE_PENDING,
			Reason:     contentMarker,
		},
		result: &controlv1.ActionResult{
			RequestId:             "req-from-payload",
			ExecutionId:           resultExecutionID,
			Status:                controlv1.ResultStatus_RESULT_STATUS_SUCCESS,
			RedactedResultPreview: contentMarker,
		},
		finding: &controlv1.Finding{
			FindingId:         "find-1",
			RequestId:         "req-from-payload",
			RunId:             "run-from-payload",
			Source:            controlv1.FindingSource_FINDING_SOURCE_DETERMINISTIC,
			RecommendedAction: contentMarker,
		},
		bundle: &controlv1.PolicyBundleRef{BundleId: "bundle-" + contentMarker, Version: "3"},
	}
}

// The eleven rows stay one list. Splitting them by category is where a missing
// row would hide, and TestTableCoversEveryConstructorAndKind checks this list
// against the methods of Builder and against every declared EventKind.
func cases() []constructorCase {
	p := newPayloads()
	return []constructorCase{
		{
			method: "Proposed", kind: controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED,
			arm: "proposed", payload: p.env,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.Proposed(p.env) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.Proposed(nil) },
		},
		{
			method: "Decided", kind: controlv1.EventKind_EVENT_KIND_POLICY_DECIDED,
			arm: "decision", payload: p.decision,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.Decided(p.decision) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.Decided(nil) },
		},
		{
			method: "ApprovalRequested", kind: controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED,
			arm: "approval", payload: p.approval,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.ApprovalRequested(p.approval) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.ApprovalRequested(nil) },
		},
		{
			method: "ApprovalDecided", kind: controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED,
			arm: "approval", payload: p.approval,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.ApprovalDecided(p.approval) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.ApprovalDecided(nil) },
		},
		{
			method: "Started", kind: controlv1.EventKind_EVENT_KIND_ACTION_STARTED,
			executionID: startedExecutionID,
			call:        func(b *evidence.Builder) *controlv1.Event { return b.Started(startedExecutionID) },
			callEmpty:   func(b *evidence.Builder) *controlv1.Event { return b.Started("") },
		},
		{
			method: "Completed", kind: controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED,
			arm: "result", payload: p.result, closes: true,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.Completed(p.result) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.Completed(nil) },
		},
		{
			method: "Failed", kind: controlv1.EventKind_EVENT_KIND_ACTION_FAILED,
			arm: "result", payload: p.result, closes: true,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.Failed(p.result) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.Failed(nil) },
		},
		{
			method: "Blocked", kind: controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED,
			arm: "decision", payload: p.decision,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.Blocked(p.decision) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.Blocked(nil) },
		},
		{
			method: "ApprovalExpired", kind: controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED,
			arm: "approval", payload: p.approval,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.ApprovalExpired(p.approval) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.ApprovalExpired(nil) },
		},
		{
			method: "FindingRaised", kind: controlv1.EventKind_EVENT_KIND_FINDING_RAISED,
			arm: "finding", payload: p.finding,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.FindingRaised(p.finding) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.FindingRaised(nil) },
		},
		{
			method: "PolicyReloaded", kind: controlv1.EventKind_EVENT_KIND_POLICY_RELOADED,
			arm: "policy", payload: p.bundle,
			call:      func(b *evidence.Builder) *controlv1.Event { return b.PolicyReloaded(p.bundle) },
			callEmpty: func(b *evidence.Builder) *controlv1.Event { return b.PolicyReloaded(nil) },
		},
	}
}

// payloadArms lists the payload arms set on an event. Every arm is read, not
// only the expected one: a test that looked at one arm would pass on a builder
// that also filled another.
func payloadArms(e *controlv1.Event) string {
	arms := []struct {
		name string
		set  bool
	}{
		{"proposed", e.GetProposed() != nil},
		{"decision", e.GetDecision() != nil},
		{"approval", e.GetApproval() != nil},
		{"result", e.GetResult() != nil},
		{"finding", e.GetFinding() != nil},
		{"policy", e.GetPolicy() != nil},
	}
	set := make([]string, 0, len(arms))
	for _, arm := range arms {
		if arm.set {
			set = append(set, arm.name)
		}
	}
	return strings.Join(set, ",")
}

// armMessage returns the message in the set payload arm, or nil when no arm is
// set.
func armMessage(e *controlv1.Event) proto.Message {
	switch payload := e.GetPayload().(type) {
	case *controlv1.Event_Proposed:
		return payload.Proposed
	case *controlv1.Event_Decision:
		return payload.Decision
	case *controlv1.Event_Approval:
		return payload.Approval
	case *controlv1.Event_Result:
		return payload.Result
	case *controlv1.Event_Finding:
		return payload.Finding
	case *controlv1.Event_Policy:
		return payload.Policy
	default:
		return nil
	}
}

// What every event carries whatever its kind.
//
// Invariant 10: an event that reaches a reader without its correlation
// identifiers cannot be joined to anything, so no kind may omit one, and none
// may take one from the payload instead of from the Builder.
//
// The mode is here rather than per row because a field set on one kind out of
// eleven would make UNSPECIFIED the usual reading of the field that says
// whether a DENY was enforced or only observed. The whole stamp is a parameter
// and not a constant read here, so that the assertion can tell what the Builder
// was given from a literal written into next().
func checkStamped(t *testing.T, e *controlv1.Event, s stamp) {
	t.Helper()
	got := evidence.IDs{
		RequestID: e.GetRequestId(),
		RunID:     e.GetRunId(),
		ProjectID: e.GetProjectId(),
		TenantID:  e.GetTenantId(),
	}
	if got != s.ids {
		t.Errorf("correlation identifiers: got %+v, want %+v from the Builder", got, s.ids)
	}
	if e.GetSchemaVersion() != "1.0" {
		t.Errorf("schema_version: got %q, want %q", e.GetSchemaVersion(), "1.0")
	}
	if e.GetKind() == controlv1.EventKind_EVENT_KIND_UNSPECIFIED {
		t.Error("kind is UNSPECIFIED, which reads as an event that says nothing about itself")
	}
	if e.GetEnforcementMode() != s.mode {
		t.Errorf("enforcement_mode: got %s, want %s from the Builder", e.GetEnforcementMode(), s.mode)
	}
}

// idsWith is a complete set of identifiers with one substitution, so a row
// tests the rule it names and nothing else.
func idsWith(requestID string) evidence.IDs {
	ids := testIDs()
	ids.RequestID = requestID
	return ids
}

// Invariant 10 holds by construction only if the constructor can refuse. The
// control case is part of the table: without it, a NewBuilder that rejected
// everything would pass this test.
func TestNewBuilderRefusesIncompleteInput(t *testing.T) {
	complete := testIDs()
	withoutRunID := complete
	withoutRunID.RunID = ""

	for _, tc := range []struct {
		name      string
		ids       evidence.IDs
		mode      controlv1.EnforcementMode
		clock     func() time.Time
		newID     func() string
		wantNames []string // substrings the error has to name; empty means accept
	}{
		{name: "complete", ids: complete, mode: modeEnforce, clock: stubClock(), newID: stubIDs("evt")},
		{
			// A run groups requests and an action need not belong to one.
			name: "no run id is accepted", ids: withoutRunID, mode: modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
		},
		{
			// The least authority a plane can exercise is still a statement
			// about what it did, so OBSERVE is accepted like any other mode.
			name: "observe is a mode", ids: complete,
			mode:  controlv1.EnforcementMode_ENFORCEMENT_MODE_OBSERVE,
			clock: stubClock(), newID: stubIDs("evt"),
		},
		{
			name: "no request id", ids: evidence.IDs{ProjectID: "p", TenantID: "t"}, mode: modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"IDs.RequestID"},
		},
		{
			name: "blank request id", ids: evidence.IDs{RequestID: "  \t ", ProjectID: "p", TenantID: "t"},
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"IDs.RequestID"},
		},
		// request_id is the scope of the chain, so a spelling difference
		// nobody can see is a trail split in two with no gap to show for it.
		{
			name: "request id padded with spaces", ids: idsWith(" req-1 "),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "white space"},
		},
		{
			name: "request id padded with a tab", ids: idsWith("req-1\t"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "white space"},
		},
		{
			name: "request id is a zero width space", ids: idsWith("\u200b"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "format character"},
		},
		{
			name: "request id carries a byte order mark", ids: idsWith("req-\ufeff1"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "format character"},
		},
		{
			name: "request id carries a NUL", ids: idsWith("req-\x001"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "control character"},
		},
		{
			name: "request id carries a soft hyphen", ids: idsWith("req\u00ad1"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "format character"},
		},
		{
			name: "request id carries a word joiner", ids: idsWith("req\u20601"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "format character"},
		},
		{
			name: "request id carries a mongolian vowel separator", ids: idsWith("req\u180e1"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "format character"},
		},
		{
			name: "request id carries a left-to-right mark", ids: idsWith("req\u200e1"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "format character"},
		},
		{
			// EncodeJSONL cannot write it, so a Builder that accepted it would
			// make records that never leave the process that built them.
			name: "request id is not valid utf-8", ids: idsWith("req-\xff\xfe"),
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RequestID", "UTF-8"},
		},
		{
			// RunID may be absent, and a present one is held to the same rule.
			name: "run id is three spaces", ids: evidence.IDs{
				RequestID: "req-1", RunID: "   ", ProjectID: "p", TenantID: "t",
			},
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RunID"},
		},
		{
			name: "run id carries a zero width space", ids: evidence.IDs{
				RequestID: "req-1", RunID: "run\u200b1", ProjectID: "p", TenantID: "t",
			},
			mode:  modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"),
			wantNames: []string{"IDs.RunID", "format character"},
		},
		{
			// A space inside an identifier is visible and joins to itself, so
			// it is a caller's business and not this package's.
			name: "a space inside an identifier is accepted", ids: idsWith("req 1"),
			mode: modeEnforce, clock: stubClock(), newID: stubIDs("evt"),
		},
		{
			name: "no project id", ids: evidence.IDs{RequestID: "r", TenantID: "t"}, mode: modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"IDs.ProjectID"},
		},
		{
			name: "no tenant id", ids: evidence.IDs{RequestID: "r", ProjectID: "p"}, mode: modeEnforce,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"IDs.TenantID"},
		},
		{
			// A plane that cannot say what authority it was exercising has
			// nothing to say about what it enforced.
			name: "unspecified mode", ids: complete,
			mode:  controlv1.EnforcementMode_ENFORCEMENT_MODE_UNSPECIFIED,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"mode"},
		},
		{
			// One past the highest mode the contract declares, which is where
			// a check that refused only UNSPECIFIED lets a number through. No
			// v1 reader can name it, so it says as little as UNSPECIFIED does,
			// and every event stamped with it would read as undetermined.
			name: "undeclared mode past the last one", ids: complete,
			mode:  controlv1.EnforcementMode_ENFORCEMENT_MODE_LOCKDOWN + 1,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"mode"},
		},
		{
			name: "negative mode", ids: complete,
			mode:  -1,
			clock: stubClock(), newID: stubIDs("evt"), wantNames: []string{"mode"},
		},
		{
			name: "nil clock", ids: complete, mode: modeEnforce, clock: nil, newID: stubIDs("evt"),
			wantNames: []string{"clock"},
		},
		{
			name: "nil id generator", ids: complete, mode: modeEnforce, clock: stubClock(), newID: nil,
			wantNames: []string{"newID"},
		},
		{
			name: "nothing at all", ids: evidence.IDs{}, clock: nil, newID: nil,
			wantNames: []string{"IDs.RequestID", "IDs.ProjectID", "IDs.TenantID", "mode", "clock", "newID"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := evidence.NewBuilder(tc.ids, tc.mode, tc.clock, tc.newID)

			if len(tc.wantNames) == 0 {
				if err != nil {
					t.Fatalf("NewBuilder: got error %v, want a Builder", err)
				}
				if b == nil {
					t.Fatal("NewBuilder returned no error and no Builder")
				}
				return
			}

			if err == nil {
				t.Fatalf("NewBuilder: got no error, want one naming %v", tc.wantNames)
			}
			if b != nil {
				t.Error("NewBuilder returned a Builder alongside an error")
			}
			if !errors.Is(err, evidence.ErrInvalidInput) {
				t.Errorf("error %v does not match evidence.ErrInvalidInput", err)
			}
			// An error that does not say what was missing sends a caller
			// looking through five arguments by hand.
			for _, name := range tc.wantNames {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name the missing %s", err, name)
				}
			}
		})
	}
}

// Refused, never rewritten. An identifier NewBuilder accepts reaches the record
// byte for byte: a Builder that trimmed or normalised one would make the
// event's own request_id stop matching the same id inside the payload it
// carries, and a reader joining the two would find nothing.
func TestAcceptedIdentifiersReachTheEventUnchanged(t *testing.T) {
	for _, requestID := range []string{"req 1", "req-\u00e9-1", "REQ-1", "  req-1", "req-1  "} {
		t.Run(requestID, func(t *testing.T) {
			ids := idsWith(requestID)
			b, err := evidence.NewBuilder(ids, modeEnforce, stubClock(), stubIDs("evt"))
			if err != nil {
				// The padded spellings are refused; that is the other half of
				// this rule and TestNewBuilderRefusesIncompleteInput holds it.
				if requestID == strings.TrimSpace(requestID) {
					t.Fatalf("NewBuilder refused %q, which carries no padding: %v", requestID, err)
				}
				return
			}
			if got := b.Started("exec-1").GetRequestId(); got != requestID {
				t.Errorf("request_id: got %q, want %q unchanged", got, requestID)
			}
		})
	}
}

// A Builder that did not come from NewBuilder has no clock to read. The panic
// is a wiring fault rather than a path this package supports, and it is pinned
// here because the alternative is a zero value that would put evidence with no
// time and no id into a trail.
func TestABuilderMadeAsALiteralPanicsOnFirstUse(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Error("a Builder built as a composite literal produced an event; it has no clock and no generator")
		}
	}()
	_ = (&evidence.Builder{}).Started("exec-1")
}

// The Builder built from an empty RunID has to work, not merely construct.
func TestEmptyRunIDReachesTheEvent(t *testing.T) {
	ids := testIDs()
	ids.RunID = ""
	b, err := evidence.NewBuilder(ids, modeEnforce, stubClock(), stubIDs("evt"))
	if err != nil {
		t.Fatalf("NewBuilder without a run id: %v", err)
	}

	e := b.Started("exec-1")
	if e.GetRunId() != "" {
		t.Errorf("run_id: got %q, want empty", e.GetRunId())
	}
	if e.GetRequestId() != ids.RequestID || e.GetProjectId() != ids.ProjectID || e.GetTenantId() != ids.TenantID {
		t.Errorf("the other identifiers did not survive an empty run id: %+v", e)
	}
}

// Under two stamps, because one stamp is five constants the assertion could be
// comparing against themselves: a next() that wrote a literal ENFORCE, a
// NewBuilder that dropped its mode argument, or a next() that wrote the literal
// "proj-from-builder", each passes a suite built from one stamp. The second
// stamp is what makes "from the Builder" mean anything.
func TestEveryConstructorStampsIdentifiers(t *testing.T) {
	for _, s := range stamps() {
		for _, tc := range cases() {
			t.Run(s.name+"/"+tc.method, func(t *testing.T) {
				stampedEventChecks(t, s, tc)
			})
		}
	}
}

// stampedEventChecks is one row of the table above under one stamp: the fields
// every event carries, whatever kind it is and whatever the plane was built
// with.
func stampedEventChecks(t *testing.T, s stamp, tc constructorCase) {
	t.Helper()
	e := tc.call(mustBuilder(t, s, "evt"))

	checkStamped(t, e, s)
	if e.GetEventId() != "evt-1" {
		t.Errorf("event_id: got %q, want %q from the injected generator", e.GetEventId(), "evt-1")
	}
	if got := e.GetOccurredAt().AsTime(); !got.Equal(baseTime()) {
		t.Errorf("occurred_at: got %s, want %s from the injected clock", got, baseTime())
	}
	if e.GetPrevEventId() != "" {
		t.Errorf("prev_event_id on the first event: got %q, want empty", e.GetPrevEventId())
	}
}

// The matrix above is worth running only if the two stamps really differ. A
// second stamp that shared a field with the first would put that field back to
// one constant everywhere without changing a single test name, which is the
// defect this file has now grown twice.
func TestTheTwoStampsShareNoValue(t *testing.T) {
	if len(stamps()) < 2 {
		t.Fatalf("stamps() returns %d stamps; the matrix needs at least two", len(stamps()))
	}
	a, b := enforcing(), approving()
	if a.name == b.name {
		t.Errorf("both stamps are named %q, so their subtests are indistinguishable", a.name)
	}
	if a.mode == b.mode {
		t.Errorf("both stamps carry mode %s, so a literal mode in next() would survive", a.mode)
	}
	for _, field := range []struct{ name, first, second string }{
		{"RequestID", a.ids.RequestID, b.ids.RequestID},
		{"RunID", a.ids.RunID, b.ids.RunID},
		{"ProjectID", a.ids.ProjectID, b.ids.ProjectID},
		{"TenantID", a.ids.TenantID, b.ids.TenantID},
	} {
		if field.first == field.second {
			t.Errorf("both stamps carry %s %q, so a literal %s in next() would survive",
				field.name, field.first, field.name)
		}
	}
}

// A Builder has no opinion about which mode a plane may be in. Every mode the
// contract declares is accepted and stamped as given, so the field cannot come
// from anywhere but the argument.
func TestEveryDeclaredModeIsStampedAsGiven(t *testing.T) {
	modes := declaredModes()
	if len(modes) < 2 {
		t.Fatalf("the contract declares %d usable modes; this test needs at least two", len(modes))
	}
	for _, mode := range modes {
		t.Run(mode.String(), func(t *testing.T) {
			s := stamp{name: mode.String(), ids: testIDs(), mode: mode}
			e := mustBuilder(t, s, "evt").Started("exec-1")
			if e.GetEnforcementMode() != mode {
				t.Errorf("enforcement_mode: got %s, want %s", e.GetEnforcementMode(), mode)
			}
		})
	}
}

// constructors lists the exported methods of the Builder type that build an
// event: every one but Position, the one reader, which resume_test.go holds.
func constructors(builderType reflect.Type) []string {
	var names []string
	for i := range builderType.NumMethod() {
		if name := builderType.Method(i).Name; name != "Position" {
			names = append(names, name)
		}
	}
	return names
}

// The table is the only thing that makes the tests above cover all eleven
// kinds. Without this test, a twelfth constructor would pass every other test
// in the file by not being in it.
//
// The table is the list of constructors, and every exported method of Builder
// is expected to be one. An exported method that is not a constructor needs
// this guard adjusted rather than a fake row.
func TestTableCoversEveryConstructorAndKind(t *testing.T) {
	rows := cases()
	byMethod := make(map[string]constructorCase, len(rows))
	for _, tc := range rows {
		if _, dup := byMethod[tc.method]; dup {
			t.Errorf("cases() has two rows for %s; one of them shadows the other", tc.method)
		}
		byMethod[tc.method] = tc
	}

	builderType := reflect.TypeOf(&evidence.Builder{})
	for _, name := range constructors(builderType) {
		if _, ok := byMethod[name]; !ok {
			t.Errorf("Builder.%s has no row in cases(), so no test checks its identifiers or its payload arm", name)
		}
	}
	for method := range byMethod {
		if _, ok := builderType.MethodByName(method); !ok {
			t.Errorf("cases() names Builder.%s, which does not exist", method)
		}
	}

	byKind := make(map[controlv1.EventKind]string, len(rows))
	for _, tc := range rows {
		if other, dup := byKind[tc.kind]; dup {
			t.Errorf("Builder.%s and Builder.%s both produce %s; one of them names the wrong kind",
				other, tc.method, tc.kind)
		}
		byKind[tc.kind] = tc.method
	}
	for number, name := range controlv1.EventKind_name {
		if number == 0 {
			continue
		}
		if _, ok := byKind[controlv1.EventKind(number)]; !ok {
			t.Errorf("the contract declares %s and no constructor produces it", name)
		}
	}
}

func TestConstructorFillsOneArmAndTheRightFields(t *testing.T) {
	for _, tc := range cases() {
		t.Run(tc.method, func(t *testing.T) {
			e := tc.call(mustBuilder(t, enforcing(), "evt"))

			if e.GetKind() != tc.kind {
				t.Errorf("kind: got %s, want %s", e.GetKind(), tc.kind)
			}
			if got := payloadArms(e); got != tc.arm {
				t.Errorf("payload arms set: got %q, want %q", got, tc.arm)
			}
			if tc.payload != nil && !proto.Equal(armMessage(e), tc.payload) {
				t.Errorf("the arm does not carry the message it was given:\ngot  %v\nwant %v",
					armMessage(e), tc.payload)
			}
			if e.GetExecutionId() != tc.executionID {
				t.Errorf("execution_id: got %q, want %q", e.GetExecutionId(), tc.executionID)
			}
			// The mode is checked by checkStamped for every kind. What belongs
			// here is the other half of the same rule: the producer's mode is
			// still in the payload, unchanged, and was not moved up.
			if d := e.GetDecision(); d != nil &&
				d.GetEnforcementMode() != controlv1.EnforcementMode_ENFORCEMENT_MODE_OBSERVE {
				t.Errorf("the decision's own enforcement_mode is %s, want OBSERVE from the fixture",
					d.GetEnforcementMode())
			}
		})
	}
}

// Evidence that changes when the caller edits the message it was built from is
// not evidence. ADR-0004 has events waiting in a spool and serialized well
// after the call.
//
// Each row takes its fixtures from its own cases() call. Within one call eleven
// rows share six messages, so Decided and Blocked hold the same Decision and
// all three approval rows the same Approval; resetting it in one subtest left
// the next one resetting a message that was already empty, which made the
// second assertion below pass on four of the ten rows whatever the constructor
// did. Those four included Blocked, and one Decision reused for Decided and
// Blocked is the case this test exists for.
func TestPayloadIsClonedNotAliased(t *testing.T) {
	for row := range cases() {
		tc := cases()[row]
		t.Run(tc.method, func(t *testing.T) {
			if tc.payload == nil {
				t.Skip("this constructor takes no message, so there is nothing for the caller to edit")
			}
			e := tc.call(mustBuilder(t, enforcing(), "evt"))

			if armMessage(e) == tc.payload {
				t.Fatal("the event holds the caller's message; any later edit rewrites the record")
			}
			before := proto.Clone(e)

			// Control, and it reads the size before the reset rather than
			// after. After the reset a message that was already empty and one
			// the reset emptied look the same, which is how the vacuous rows
			// went unnoticed.
			if size := proto.Size(tc.payload); size == 0 {
				t.Fatalf("the %s fixture is already empty, so resetting it proves nothing", tc.method)
			}

			// The caller wrecks its own message after the call, which is the
			// cheap universal edit: every payload type supports it.
			proto.Reset(tc.payload)

			if size := proto.Size(tc.payload); size != 0 {
				t.Fatalf("proto.Reset left %d bytes in the caller's message", size)
			}
			if !proto.Equal(e, before) {
				t.Errorf("the event changed when the caller reset its own message:\nnow    %v\nbefore %v", e, before)
			}
		})
	}
}

// Invariant 9: prompt and tool content is not copied into the record beyond the
// payload the producer already redacted. The marker only exists inside the
// fixtures, so it reaching event_id, execution_id or any other field of the
// event is a leak this test names.
func TestNoPayloadContentReachesTopLevelFields(t *testing.T) {
	marker := []byte(contentMarker)
	for _, tc := range cases() {
		t.Run(tc.method, func(t *testing.T) {
			if tc.payload == nil {
				// Started is the row this skips, and it is the one constructor
				// that writes a caller string straight into a top-level field
				// (execution_id). That is what it is for; this test is about
				// content moving out of a payload, and it does not cover it.
				t.Skip("this constructor takes no payload, so there is nothing to leak from")
			}
			e := tc.call(mustBuilder(t, enforcing(), "evt"))

			// Control: the fixture has to carry the marker, or the check below
			// passes because there was never anything to find.
			full, err := proto.Marshal(e)
			if err != nil {
				t.Fatalf("proto.Marshal: %v", err)
			}
			if !bytes.Contains(full, marker) {
				t.Fatalf("the %s fixture does not carry the marker, so this case proves nothing", tc.method)
			}

			// The payload is where content belongs. Everything else is the
			// record's own metadata.
			e.Payload = nil

			wire, err := proto.Marshal(e)
			if err != nil {
				t.Fatalf("proto.Marshal without the payload: %v", err)
			}
			if bytes.Contains(wire, marker) {
				t.Errorf("payload content reached a field outside the payload: %v", e)
			}
			// The JSON form too: a field the binary encoding skips as empty
			// would be invisible above.
			doc, err := protojson.Marshal(e)
			if err != nil {
				t.Fatalf("protojson.Marshal without the payload: %v", err)
			}
			if bytes.Contains(doc, marker) {
				t.Errorf("payload content reached a field outside the payload: %s", doc)
			}
		})
	}
}

// The Builder owns the event's execution_id. The event that closes an
// action names the execution the Builder's Started was handed, and the result's
// own id stays in the payload as the producer wrote it: the producer's
// statement beside the plane's, as the enforcement mode already is. The two
// fixture ids differ, so a Builder that lifted the result's id shows which one
// it took. Before any Started there is nothing to stamp, and ValidateChain
// refuses that trail.
func TestAClosingEventNamesTheExecutionTheBuilderStarted(t *testing.T) {
	p := newPayloads()
	completed := func(r *controlv1.ActionResult) func(b *evidence.Builder) *controlv1.Event {
		return func(b *evidence.Builder) *controlv1.Event { return b.Completed(r) }
	}
	failed := func(r *controlv1.ActionResult) func(b *evidence.Builder) *controlv1.Event {
		return func(b *evidence.Builder) *controlv1.Event { return b.Failed(r) }
	}
	for _, tc := range []struct {
		name     string
		start    bool // whether the Builder records a Started first
		close    func(b *evidence.Builder) *controlv1.Event
		want     string // the event's own execution_id
		inResult string // the result's own execution_id, "" when there is no result
	}{
		{"Completed, the result naming its own execution", true, completed(p.result), startedExecutionID, resultExecutionID},
		{"Failed, the result naming its own execution", true, failed(p.result), startedExecutionID, resultExecutionID},
		{"Completed with no result", true, completed(nil), startedExecutionID, ""},
		{"Failed with no result", true, failed(nil), startedExecutionID, ""},
		{"Completed before anything started", false, completed(p.result), "", resultExecutionID},
		{"Failed before anything started", false, failed(p.result), "", resultExecutionID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := mustBuilder(t, enforcing(), "evt")
			if tc.start {
				b.Started(startedExecutionID)
			}
			e := tc.close(b)

			if got := e.GetExecutionId(); got != tc.want {
				t.Errorf("execution_id: got %q, want %q", got, tc.want)
			}
			if got := e.GetResult().GetExecutionId(); got != tc.inResult {
				t.Errorf("the result's own execution_id: got %q, want %q as the producer wrote it", got, tc.inResult)
			}
		})
	}
}

// What this establishes: a missing payload does not panic, and the gap is
// visible as an unset arm rather than as a present, empty message. It does not
// establish that the resulting record is meaningful. An APPROVAL_DECIDED with
// no approval arm asserts that somebody answered and carries no approver, no
// state and no action digest; a reader has to treat it as INDETERMINATE
// (invariant 4), which is validation this package cannot do because a
// constructor has no way to refuse.
//
// Each call follows a Started, which is where a nil result is recorded: a
// crash or a timeout leaves the caller no result to pass to Failed. The event
// still names what ran, because the execution is the Builder's to stamp and
// not the result's.
func TestEmptyPayloadLeavesTheArmUnset(t *testing.T) {
	for _, tc := range cases() {
		t.Run(tc.method, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Builder.%s with an empty payload panicked: %v", tc.method, r)
				}
			}()
			b := mustBuilder(t, enforcing(), "nil")
			b.Started(startedExecutionID)
			e := tc.callEmpty(b)

			checkStamped(t, e, enforcing())
			// Without this the whole column could be one constructor: every
			// other assertion here holds for any kind, so eleven rows would
			// demonstrate the rule for as few as one.
			if e.GetKind() != tc.kind {
				t.Errorf("kind: got %s, want %s", e.GetKind(), tc.kind)
			}
			if e.GetPayload() != nil {
				t.Errorf("payload oneof is set to %T; a missing payload must leave it unset", e.GetPayload())
			}
			// A closing event names the execution Started was handed. No other
			// kind names one, and Started's own empty call names an empty one.
			want := ""
			if tc.closes {
				want = startedExecutionID
			}
			if e.GetExecutionId() != want {
				t.Errorf("execution_id: got %q, want %q", e.GetExecutionId(), want)
			}
			if _, err := proto.Marshal(e); err != nil {
				t.Errorf("proto.Marshal: %v", err)
			}
		})
	}
}

// The nil rule covers a nil pointer and nothing else. A caller that builds a
// message and forgets to fill it gets it recorded as given: a present decision
// reading VERDICT_UNSPECIFIED, which the contract defines as "nothing was
// decided". Pinned here so the limit is known rather than assumed, and so the
// obligation is visible: a reader treats it as INDETERMINATE, never as a
// decision.
func TestEmptyMessagePayloadIsRecordedAsGiven(t *testing.T) {
	e := mustBuilder(t, enforcing(), "evt").Decided(&controlv1.Decision{})

	if e.GetPayload() == nil {
		t.Fatal("an empty but present message left the arm unset; the nil rule has grown a second meaning")
	}
	if got := e.GetDecision().GetVerdict(); got != controlv1.Verdict_VERDICT_UNSPECIFIED {
		t.Errorf("verdict: got %s, want VERDICT_UNSPECIFIED", got)
	}
}

func TestPrevEventIDLinksTheSequence(t *testing.T) {
	rows := cases()
	b := mustBuilder(t, enforcing(), "evt")

	seen := make(map[string]bool, len(rows))
	prev := ""
	for i, tc := range rows {
		e := tc.call(b)
		if e.GetPrevEventId() != prev {
			t.Fatalf("event %d (%s): prev_event_id is %q, want %q", i, tc.method, e.GetPrevEventId(), prev)
		}
		if e.GetEventId() == "" {
			t.Fatalf("event %d (%s): event_id is empty", i, tc.method)
		}
		if seen[e.GetEventId()] {
			t.Fatalf("event %d (%s): event_id %q was already used", i, tc.method, e.GetEventId())
		}
		seen[e.GetEventId()] = true
		prev = e.GetEventId()
	}

	// A second Builder over the same identifiers starts a new sequence: the
	// link is per Builder, and nothing in this package reads a stored trail to
	// continue one.
	if got := mustBuilder(t, enforcing(), "evt").Started("exec-9").GetPrevEventId(); got != "" {
		t.Errorf("first event of a new Builder: prev_event_id is %q, want empty", got)
	}
}

// newID is a trusted input and the Builder cannot check it, so what a broken
// generator produces is pinned here rather than left to be discovered. Both
// shapes are indistinguishable from a correct trail by shape alone, which is
// why validation downstream rejects an empty event_id and a self link instead
// of trusting the link.
func TestDegenerateIDGeneratorProducesAnUncheckableTrail(t *testing.T) {
	empty, err := evidence.NewBuilder(testIDs(), modeEnforce, stubClock(), func() string { return "" })
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	for i := range 3 {
		e := empty.Started("exec-1")
		if e.GetEventId() != "" || e.GetPrevEventId() != "" {
			t.Fatalf("event %d: got event_id %q prev_event_id %q, want both empty: an empty generator "+
				"makes every event look like the head of a trail",
				i, e.GetEventId(), e.GetPrevEventId())
		}
	}

	repeating, err := evidence.NewBuilder(testIDs(), modeEnforce, stubClock(), func() string { return "same" })
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	repeating.Started("exec-1")
	second := repeating.Started("exec-2")
	if second.GetEventId() != second.GetPrevEventId() {
		t.Fatalf("a repeating generator: got event_id %q prev_event_id %q, want an event that is its own predecessor",
			second.GetEventId(), second.GetPrevEventId())
	}
}

// The other trusted input. A clock outside the years protobuf can represent
// builds a record the binary encoding accepts and the JSON encoding refuses,
// which is evidence lost at export time rather than at build time. NewBuilder
// cannot check a function it has not called yet and a constructor cannot
// refuse, so the shape is pinned here: a writer reports the serialization
// error, it never skips the record.
func TestOutOfRangeClockBuildsARecordJSONRefuses(t *testing.T) {
	far := time.Date(12026, 1, 1, 0, 0, 0, 0, time.UTC)
	b, err := evidence.NewBuilder(testIDs(), modeEnforce, func() time.Time { return far }, stubIDs("evt"))
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}

	e := b.Started("exec-1")
	if e.GetOccurredAt().IsValid() {
		t.Fatalf("occurred_at %v is valid, so this case no longer tests what it says", e.GetOccurredAt())
	}
	if _, err := proto.Marshal(e); err != nil {
		t.Errorf("the binary encoding refused the record too: %v", err)
	}
	if _, err := protojson.Marshal(e); err == nil {
		t.Error("protojson accepted an out-of-range timestamp; the hazard this pins has changed")
	}
}

// Determinism is the property that lets a reviewer replay a sequence from its
// recorded inputs. Deterministic marshalling is asked for explicitly because
// the default protobuf output is not required to be byte-stable.
func TestSequenceIsReproducible(t *testing.T) {
	run := func() [][]byte {
		b := mustBuilder(t, enforcing(), "evt")
		out := make([][]byte, 0, len(cases()))
		for _, tc := range cases() {
			wire, err := (proto.MarshalOptions{Deterministic: true}).Marshal(tc.call(b))
			if err != nil {
				t.Fatalf("%s: proto.Marshal: %v", tc.method, err)
			}
			out = append(out, wire)
		}
		return out
	}

	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("two runs produced %d and %d events", len(first), len(second))
	}
	for i := range first {
		if !bytes.Equal(first[i], second[i]) {
			t.Errorf("event %d differs between two identical runs:\n%x\n%x", i, first[i], second[i])
		}
	}

	// The clock advances one second per call, so the last event of an eleven
	// event sequence has to be ten seconds after the first. An implementation
	// that read the clock once and reused the value would pass the byte
	// comparison above.
	b := mustBuilder(t, enforcing(), "evt")
	for i, tc := range cases() {
		want := baseTime().Add(time.Duration(i) * time.Second)
		if got := tc.call(b).GetOccurredAt().AsTime(); !got.Equal(want) {
			t.Errorf("event %d (%s): occurred_at is %s, want %s", i, tc.method, got, want)
		}
	}
}

// The clock and the id generator are arguments so that this package cannot
// reach for the wall clock or for a random value. Either one added later would
// be invisible to every other test here, because they all inject both.
func TestPackageSourceReadsNoClockOrRandomness(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	fset := token.NewFileSet()
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		checkNoRandomness(t, name, file)
		checkNoWallClock(t, fset, name, file)
	}
	// A scan that read nothing is a broken test, not a clean package.
	if scanned == 0 {
		t.Fatal("no non-test Go file was scanned; this check examined nothing")
	}
}

func checkNoRandomness(t *testing.T, name string, file *ast.File) {
	t.Helper()
	forbidden := map[string]string{
		"math/rand":    "a random value makes an event sequence unreproducible",
		"math/rand/v2": "a random value makes an event sequence unreproducible",
		"crypto/rand":  "the id generator is injected; this package draws no randomness",
	}
	for path, spec := range importPaths(t, name, file) {
		if why, bad := forbidden[path]; bad {
			t.Errorf("%s imports %q as %s: %s", name, path, importName(spec, path), why)
		}
	}
}

// The local name bound to "time" is read from the file rather than assumed: an
// import renamed to tm would walk a check that matched on the identifier "time"
// straight past a tm.Now().
func checkNoWallClock(t *testing.T, fset *token.FileSet, name string, file *ast.File) {
	t.Helper()
	locals := make(map[string]bool, 1)
	for path, spec := range importPaths(t, name, file) {
		if path != "time" {
			continue
		}
		local := importName(spec, path)
		if local == "." {
			t.Errorf("%s dot-imports time, which leaves this check nothing to match on", name)
			continue
		}
		locals[local] = true
	}
	if len(locals) == 0 {
		return
	}

	// Everything in time that reads the clock rather than doing arithmetic on a
	// value the caller already has.
	reads := map[string]bool{
		"Now": true, "Since": true, "Until": true, "After": true, "AfterFunc": true,
		"Tick": true, "NewTimer": true, "NewTicker": true,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || !locals[pkg.Name] {
			return true
		}
		if reads[sel.Sel.Name] {
			t.Errorf("%s: %s.%s reads the wall clock; the clock arrives as an argument to NewBuilder",
				fset.Position(sel.Pos()), pkg.Name, sel.Sel.Name)
		}
		return true
	})
}

// importPaths maps each imported path to its spec. A path that does not unquote
// is fatal: it means this scan is reading something that is not the file the
// compiler read.
func importPaths(t *testing.T, name string, file *ast.File) map[string]*ast.ImportSpec {
	t.Helper()
	paths := make(map[string]*ast.ImportSpec, len(file.Imports))
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: unquote import %s: %v", name, spec.Path.Value, err)
		}
		paths[path] = spec
	}
	return paths
}

// importName is the identifier the file binds an import to: the rename when
// there is one, the last path segment otherwise.
func importName(spec *ast.ImportSpec, path string) string {
	if spec.Name != nil {
		return spec.Name.Name
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Whatever order a caller uses, and however long the trail is, every event
// carries the four identifiers and the schema version, no event_id repeats,
// prev_event_id names exactly the event before it, and an event that closes an
// action names the execution the last Started was handed, whatever its result
// says.
func FuzzBuilderChain(f *testing.F) {
	f.Add(uint8(0), uint8(0), []byte{0})
	f.Add(uint8(1), uint8(1), []byte{0, 1, 4, 5})
	f.Add(uint8(2), uint8(1), []byte{10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0})
	f.Add(uint8(5), uint8(0), []byte{3, 3, 3, 8, 8})

	f.Fuzz(func(t *testing.T, modeChoice, idsChoice uint8, choices []byte) {
		// An empty input would assert nothing at all, and a very long one only
		// repeats what the first hundred calls established.
		if len(choices) == 0 {
			choices = []byte{0}
		}
		if len(choices) > 128 {
			choices = choices[:128]
		}

		// The stamp is drawn too: an assertion against the one stamp every
		// Builder is made with cannot tell a stamped field from a literal. The
		// mode is drawn over every declared mode and not only the two the table
		// uses, because a Builder accepts all of them.
		modes := declaredModes()
		sets := []evidence.IDs{testIDs(), otherIDs()}
		drawn := stamp{
			ids:  sets[int(idsChoice)%len(sets)],
			mode: modes[int(modeChoice)%len(modes)],
		}

		rows := cases()
		b := mustBuilder(t, drawn, "evt")
		seen := make(map[string]bool, len(choices))
		prev := ""
		started := "" // what the last Started was handed, which a closing event names
		for i, choice := range choices {
			tc := rows[int(choice)%len(rows)]
			e := tc.call(b)

			checkStamped(t, e, drawn)
			if e.GetPrevEventId() != prev {
				t.Fatalf("event %d (%s): prev_event_id is %q, want %q", i, tc.method, e.GetPrevEventId(), prev)
			}
			if seen[e.GetEventId()] {
				t.Fatalf("event %d (%s): event_id %q was already used", i, tc.method, e.GetEventId())
			}
			if got := payloadArms(e); got != tc.arm {
				t.Fatalf("event %d (%s): payload arms set are %q, want %q", i, tc.method, got, tc.arm)
			}
			want := tc.executionID
			if tc.closes {
				want = started
			}
			if got := e.GetExecutionId(); got != want {
				t.Fatalf("event %d (%s): execution_id is %q, want %q", i, tc.method, got, want)
			}
			if tc.kind == controlv1.EventKind_EVENT_KIND_ACTION_STARTED {
				started = tc.executionID
			}
			seen[e.GetEventId()] = true
			prev = e.GetEventId()
		}
	})
}
