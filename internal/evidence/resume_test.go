package evidence_test

import (
	"errors"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/pkg/contract"
)

// TestResumeContinuesTheChain: a trail held after APPROVAL_REQUESTED and
// resumed by a second Builder is one chain, links intact, and the event that
// closes the action names the execution the resumed Builder started.
func TestResumeContinuesTheChain(t *testing.T) {
	first, err := evidence.NewBuilder(testIDs(), modeEnforce, stubClock(), stubIDs("a"))
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	events := []*controlv1.Event{
		first.Proposed(&controlv1.ActionEnvelope{}),
		first.Decided(&controlv1.Decision{}),
		first.ApprovalRequested(&controlv1.Approval{}),
	}
	at := first.Position()
	if at.LastEventID != "a-3" || at.ExecutionID != "" {
		t.Fatalf("Position after three events = %+v, want last a-3 and no execution", at)
	}

	second, err := evidence.Resume(testIDs(), modeEnforce, stubClock(), stubIDs("b"), at)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	events = append(events,
		second.ApprovalDecided(&controlv1.Approval{}),
		second.Started("exec-1"),
		second.Completed(&controlv1.ActionResult{}),
	)
	if got := events[3].GetPrevEventId(); got != "a-3" {
		t.Errorf("the first resumed event links to %q, want a-3", got)
	}
	if got := events[5].GetExecutionId(); got != "exec-1" {
		t.Errorf("the closing event names execution %q, want exec-1", got)
	}
	if err := evidence.ValidateChain(events); err != nil {
		t.Errorf("ValidateChain over a resumed trail: %v", err)
	}
	if got := second.Position(); got != (evidence.Position{LastEventID: "b-3", ExecutionID: "exec-1"}) {
		t.Errorf("Position after the resume = %+v", got)
	}
}

// TestResumeAfterStartClosesTheExecutionItWasGiven: a trail resumed after
// ACTION_STARTED, across a restart, closes with the execution it was told
// about, not with an empty one ValidateChain would refuse.
func TestResumeAfterStartClosesTheExecutionItWasGiven(t *testing.T) {
	first, err := evidence.NewBuilder(testIDs(), modeEnforce, stubClock(), stubIDs("a"))
	if err != nil {
		t.Fatalf("NewBuilder: %v", err)
	}
	events := []*controlv1.Event{
		first.Proposed(&controlv1.ActionEnvelope{}),
		first.Decided(&controlv1.Decision{}),
		first.Started("exec-7"),
	}
	second, err := evidence.Resume(testIDs(), modeEnforce, stubClock(), stubIDs("b"), first.Position())
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	events = append(events, second.Failed(&controlv1.ActionResult{}))
	if got := events[3].GetExecutionId(); got != "exec-7" {
		t.Errorf("the closing event names execution %q, want exec-7", got)
	}
	if err := evidence.ValidateChain(events); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
}

// TestResumeRefusesWhatItCannotContinue: every refusal of NewBuilder, plus a
// position with no last event, which is not a resume, and identifiers over
// the contract's bound, which ValidateChain would refuse on the next event.
func TestResumeRefusesWhatItCannotContinue(t *testing.T) {
	long := strings.Repeat("x", contract.MaxStringBytes+1)
	cases := []struct {
		name string
		ids  evidence.IDs
		mode controlv1.EnforcementMode
		at   evidence.Position
		want string
	}{
		{"no last event", testIDs(), modeEnforce, evidence.Position{}, "LastEventID is empty"},
		{"a last event id over the bound", testIDs(), modeEnforce, evidence.Position{LastEventID: long}, "LastEventID is"},
		{"an execution id over the bound", testIDs(), modeEnforce, evidence.Position{LastEventID: "a-3", ExecutionID: long}, "ExecutionID is"},
		{"an empty request id", evidence.IDs{ProjectID: "p", TenantID: "t"}, modeEnforce, evidence.Position{LastEventID: "a-3"}, "IDs.RequestID is empty"},
		{"an unspecified mode", testIDs(), controlv1.EnforcementMode_ENFORCEMENT_MODE_UNSPECIFIED, evidence.Position{LastEventID: "a-3"}, "mode is unspecified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := evidence.Resume(tc.ids, tc.mode, stubClock(), stubIDs("b"), tc.at)
			if b != nil || !errors.Is(err, evidence.ErrInvalidInput) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Resume = %v, %v; want nil, ErrInvalidInput naming %q", b, err, tc.want)
			}
		})
	}
	if _, err := evidence.Resume(testIDs(), modeEnforce, nil, nil, evidence.Position{LastEventID: "a-3"}); !errors.Is(err, evidence.ErrInvalidInput) {
		t.Errorf("Resume with no clock and no id source = %v, want ErrInvalidInput", err)
	}
}
