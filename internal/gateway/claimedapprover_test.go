package gateway_test

import (
	"errors"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
)

// claimedApprovers are approver ids a store may answer with: a plain claim the
// plane records, and three that read as the reserved authn: prefix, which it
// never takes from a store.
var claimedApprovers = []struct {
	id       string
	reserved bool
}{
	{"night-officer", false},
	{"authn:roster/x:mallory", true},
	{"AUTHN:roster/x:mallory", true},
	{"аuthn:x", true},
}

// TestTheResumeHoldsTheStoresApproverToTheClaimRule: a store that spends the
// held approval and answers it as approved by an id the claim rule refuses
// runs nothing. The held trail closes with the plane's own approval, expired,
// and never records that id; the same answer naming a plain claim runs and
// records it, so the reserved prefix is what changes the outcome.
func TestTheResumeHoldsTheStoresApproverToTheClaimRule(t *testing.T) {
	for _, c := range claimedApprovers {
		t.Run(c.id, func(t *testing.T) {
			f := newFakeStore()
			h := withStore(t, f)
			holdApproved(t, h, f)
			f.answer = func(a *controlv1.Approval, err error) (*controlv1.Approval, error) {
				if err != nil {
					t.Fatalf("the genuine Consume failed: %v", err)
				}
				a.ApproverId = c.id
				return a, nil
			}
			d := h.admit(retry(t, "req-2"), refundArgs())
			trail := h.trailOf("req-1")
			if err := evidence.ValidateChain(trail); err != nil {
				t.Errorf("ValidateChain: %v", err)
			}
			if !c.reserved {
				if d.Action != core.Execute || recordedApprover(trail) != c.id {
					t.Fatalf("a plain claim: Action %d, APPROVAL_DECIDED by %q; want Execute by %q", d.Action, recordedApprover(trail), c.id)
				}
				return
			}
			expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
			expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
			if len(trail) == 5 {
				expectPlanesApprovalExpired(t, trail[2].GetApproval(), trail[3].GetApproval())
			}
			if s := h.p.Stats(); s.Executed != 0 || s.Held != 0 || s.Blocks[codeEvidenceUnavailable] != 1 {
				t.Errorf("Stats: %d executed, %d held, blocks %v", s.Executed, s.Held, s.Blocks)
			}
		})
	}
}

// TestARejectionByAReservedApproverIsNeverRecorded: a rejection the store
// answers under an id the claim rule refuses is not written as
// APPROVAL_DECIDED either; the window closes with the plane's own approval.
func TestARejectionByAReservedApproverIsNeverRecorded(t *testing.T) {
	f := newFakeStore()
	h := withStore(t, f)
	first := hold(t, h)
	if err := f.Answer(first.Pending.ApprovalID, rejected, "bob", "not today", base()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	f.answer = func(a *controlv1.Approval, err error) (*controlv1.Approval, error) {
		if !errors.Is(err, gateway.ErrApprovalRejected) || a == nil {
			t.Fatalf("the genuine Consume: %v, %v; want the rejected record", a, err)
		}
		a.ApproverId = "authn:roster/x:mallory"
		return a, err
	}
	d := h.admit(retry(t, "req-2"), refundArgs())
	expectBlock(t, d, verdictIndeterminate, codeEvidenceUnavailable, gateway.PDPType)
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindApprovalRequested, kindApprovalExpired, kindBlocked})
	if len(trail) == 5 {
		expectPlanesApprovalExpired(t, trail[2].GetApproval(), trail[3].GetApproval())
	}
}

// TestTheReconciliationHoldsTheStoresApproverToTheClaimRule: a lost hold whose
// record the store reports approved by an id the claim rule refuses closes
// APPROVAL_STATE_UNKNOWN with the plane's own approval, as a record a store
// refuses to read does, never APPROVAL_NOT_RESUMED naming that id. A plain
// claim closes APPROVAL_NOT_RESUMED.
func TestTheReconciliationHoldsTheStoresApproverToTheClaimRule(t *testing.T) {
	for _, c := range claimedApprovers {
		t.Run(c.id, func(t *testing.T) {
			store := newFakeStore()
			first, pending, next := lostUnder(t, store, base().Add(time.Minute), func(pending gateway.Disposition) {
				answeredThen(t, store, pending, func(a *controlv1.Approval) { a.ApproverId = c.id })
			})
			if !c.reserved {
				trail := expectClosedLostHold(t, first, kindApprovalDecided, verdictDeny, codeApprovalNotResumed)
				if got := trail[3].GetApproval().GetApproverId(); got != c.id {
					t.Errorf("APPROVAL_DECIDED names %q, want %q", got, c.id)
				}
				return
			}
			trail := expectClosedLostHold(t, first, kindApprovalExpired, verdictIndeterminate, codeApprovalStateUnknown)
			expectOwnApprovalExpired(t, trail, pending)
			if s := next.p.Stats(); s.Blocks[codeApprovalStateUnknown] != 1 || len(s.Blocks) != 1 || s.Executed != 0 {
				t.Errorf("Stats = %+v; want the one close counted as APPROVAL_STATE_UNKNOWN", s)
			}
		})
	}
}

// recordedApprover is the approver APPROVAL_DECIDED names on trail, or "".
func recordedApprover(trail []*controlv1.Event) string {
	for _, e := range trail {
		if e.GetKind() == kindApprovalDecided {
			return e.GetApproval().GetApproverId()
		}
	}
	return ""
}
