package approvals_test

import (
	"errors"
	"maps"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core/approval"
)

// sibling is the approval id a writer of the directory files a second record
// under.
const sibling = "APPROVAL2"

// reservedApprover is an approver id the claim rule refuses.
const reservedApprover = "authn:okta:alice"

// forgeSibling files a copy of the framed record from under another approval
// id, for requestID, naming approverID, as a writer of the directory would.
func forgeSibling(t *testing.T, dir, from, name, requestID, approverID string) {
	t.Helper()
	b := withField(t, readFile(t, dir, from), "approval_id", `"`+sibling+`"`)
	b = withApprovalField(t, b, "approvalId", `"`+sibling+`"`)
	b = withField(t, b, "request_id", `"`+requestID+`"`)
	b = withApprovalField(t, b, "requestId", `"`+requestID+`"`)
	b = withApprovalField(t, b, "approverId", `"`+approverID+`"`)
	writeFile(t, dir, name, b)
}

// recordOf is the one record of approvalID among found.
func recordOf(t *testing.T, found []approvals.Record, approvalID string) approvals.Record {
	t.Helper()
	for _, rec := range found {
		if rec.ApprovalID == approvalID {
			return rec
		}
	}
	t.Fatalf("no record of %s among %d", approvalID, len(found))
	return approvals.Record{}
}

// expectUnknown holds a refused record to what Find may say of it: what
// identifies it and its window, and nothing about its answer or its fate.
func expectUnknown(t *testing.T, rec approvals.Record, requestID string) {
	t.Helper()
	expectUnknownOf(t, rec, sibling, requestID)
}

// expectUnknownOf is expectUnknown for the record of approvalID.
func expectUnknownOf(t *testing.T, rec approvals.Record, approvalID, requestID string) {
	t.Helper()
	a := rec.Approval
	switch {
	case rec.Resolution != approvals.ResolutionUnspecified:
		t.Errorf("the refused record reads %v, want unspecified", rec.Resolution)
	case a.GetState() != controlv1.ApprovalState_APPROVAL_STATE_UNSPECIFIED || a.GetApproverId() != "" || a.GetDecidedAt() != nil:
		t.Errorf("the refused record carries its answer: %s by %q at %v", a.GetState(), a.GetApproverId(), a.GetDecidedAt())
	case a.GetApprovalId() != approvalID || a.GetRequestId() != requestID || rec.RequestID != requestID:
		t.Errorf("the refused record names %s for %s, want %s for %s", a.GetApprovalId(), a.GetRequestId(), approvalID, requestID)
	case !a.GetRequestedAt().AsTime().Equal(minted) || !a.GetExpiresAt().AsTime().Equal(expires):
		t.Errorf("the refused record's window is %v to %v, want %v to %v", a.GetRequestedAt(), a.GetExpiresAt(), minted, expires)
	case a.GetActionDigest() == "" || a.GetPolicyBundleDigest() != string(bundle):
		t.Errorf("the refused record's digests are %q and %q", a.GetActionDigest(), a.GetPolicyBundleDigest())
	}
}

// TestARefusedRecordOfAnotherRequestBlocksNothing: a writer files a record
// for another request of the same action, under an approver id the claim rule
// refuses. Find answers about every record of the binding, the refused one as
// unknown, and the plane's own approval is spent as if the other were not
// there.
func TestARefusedRecordOfAnotherRequestBlocksNothing(t *testing.T) {
	for name, requestID := range map[string]string{"another request": "req-2", "the same request": "req-1"} {
		t.Run(name, func(t *testing.T) {
			p, dir, h := heldOne(t)
			answerAt(t, dir, minted.Add(time.Minute))
			forgeSibling(t, dir, answeredName, sibling+".1-answered.rec", requestID, reservedApprover)
			now := minted.Add(2 * time.Minute)

			found, err := p.Find(t.Context(), h.Binding, now)
			if err != nil || len(found) != 2 {
				t.Fatalf("Find = %d record(s), %v; want the plane's own and the refused one", len(found), err)
			}
			expectUnknown(t, recordOf(t, found, sibling), requestID)
			if own := recordOf(t, found, firstApproval); own.Approval.GetApproverId() != "approver-1" {
				t.Errorf("the plane's own record reads approver %q", own.Approval.GetApproverId())
			}
			if got, err := p.Consume(t.Context(), h.Binding, requestID, sibling, now); got != nil || !errors.Is(err, approval.ErrReservedApproverID) {
				t.Errorf("Consume of the refused record = %v, %v; want nothing and ErrReservedApproverID", got, err)
			}
			if got, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, now); err != nil || got.GetApproverId() != "approver-1" {
				t.Fatalf("Consume of the plane's own record = %v, %v; want it spent", got, err)
			}
			assertNames(t, dir, consumedName, sibling+".1-answered.rec")
		})
	}
}

// TestARefusedRecordPastItsExpiryIsHidden: Find hides a refused record that
// has expired, as it hides any other, so it says nothing past its window.
func TestARefusedRecordPastItsExpiryIsHidden(t *testing.T) {
	p, dir, h := heldOne(t)
	answerAt(t, dir, minted.Add(time.Minute))
	forgeSibling(t, dir, answeredName, sibling+".1-answered.rec", "req-2", reservedApprover)
	early := expires.Add(-5 * time.Minute)
	writeFile(t, dir, sibling+".1-answered.rec", withApprovalField(t, readFile(t, dir, sibling+".1-answered.rec"),
		"expiresAt", mustJSON(t, early.Format(time.RFC3339Nano))))

	found, err := p.Find(t.Context(), h.Binding, early.Add(-time.Second))
	if err != nil || len(found) != 2 {
		t.Fatalf("Find before the refused record's expiry = %d record(s), %v; want both", len(found), err)
	}
	found, err = p.Find(t.Context(), h.Binding, early)
	if err != nil || len(found) != 1 || found[0].ApprovalID != firstApproval {
		t.Errorf("Find at the refused record's expiry = %v, %v; want the plane's own record alone", found, err)
	}
	if found, err := p.Find(t.Context(), h.Binding, expires.Add(time.Hour)); !errors.Is(err, approvals.ErrNoApproval) {
		t.Errorf("Find past every expiry = %v, %v; want ErrNoApproval", found, err)
	}
}

// TestResolveClosesOnlyTheApprovalItNames: the plane's own record is refused
// for its approver id and a writer files an accepted record of the same
// request under another approval id. Resolve refuses, and leaves the other
// record byte for byte as it was.
func TestResolveClosesOnlyTheApprovalItNames(t *testing.T) {
	p, dir, h := heldOne(t)
	answerAt(t, dir, minted.Add(time.Minute))
	forgeSibling(t, dir, answeredName, sibling+".1-answered.rec", "req-1", "approver-2")
	writeFile(t, dir, answeredName, withApprovalField(t, readFile(t, dir, answeredName), "approverId", `"`+reservedApprover+`"`))
	before := dirContents(t, dir)
	now := minted.Add(2 * time.Minute)

	if err := p.Resolve(t.Context(), h.Binding, "req-1", firstApproval, approvals.ResolutionNotResumed, now); !errors.Is(err, approval.ErrReservedApproverID) {
		t.Errorf("Resolve of the refused own record = %v, want ErrReservedApproverID", err)
	}
	if err := p.Resolve(t.Context(), h.Binding, "req-1", "", approvals.ResolutionNotResumed, now); !errors.Is(err, approvals.ErrRecordName) {
		t.Errorf("Resolve naming no approval = %v, want ErrRecordName", err)
	}
	if err := p.Resolve(t.Context(), h.Binding, "req-1", "APPROVAL3", approvals.ResolutionNotResumed, now); !errors.Is(err, approvals.ErrNoApproval) {
		t.Errorf("Resolve of an approval nobody holds = %v, want ErrNoApproval", err)
	}
	if after := dirContents(t, dir); !maps.Equal(before, after) {
		t.Fatalf("Resolve changed the directory: %v, was %v", keysOf(after), keysOf(before))
	}
}

// TestASpentRecordStaysSpentBesideARefusedSibling: the plane's own record was
// consumed, and a writer files a refused record of the same request under
// another approval id, answered or consumed. The spent record still answers
// ErrApprovalConsumed to Consume and to Resolve, and Find reads the refused
// one as unknown, never as spent.
func TestASpentRecordStaysSpentBesideARefusedSibling(t *testing.T) {
	for name, sib := range map[string]struct{ from, to string }{
		"answered": {answeredName, sibling + ".1-answered.rec"},
		"consumed": {consumedName, sibling + ".3-consumed.rec"},
	} {
		t.Run(name, func(t *testing.T) {
			p, dir, h := heldOne(t)
			answerAt(t, dir, minted.Add(time.Minute))
			if sib.from == answeredName {
				forgeSibling(t, dir, sib.from, sib.to, "req-1", reservedApprover)
			}
			now := minted.Add(2 * time.Minute)
			if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, now); err != nil {
				t.Fatalf("consuming: %v", err)
			}
			if sib.from == consumedName {
				forgeSibling(t, dir, sib.from, sib.to, "req-1", reservedApprover)
			}

			if _, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, now); !errors.Is(err, approvals.ErrApprovalConsumed) {
				t.Errorf("Consume again = %v, want ErrApprovalConsumed", err)
			}
			if err := p.Resolve(t.Context(), h.Binding, "req-1", firstApproval, approvals.ResolutionNotResumed, now); !errors.Is(err, approvals.ErrApprovalConsumed) {
				t.Errorf("Resolve of the spent record = %v, want ErrApprovalConsumed", err)
			}
			found, err := p.Find(t.Context(), h.Binding, now)
			if err != nil || len(found) != 2 {
				t.Fatalf("Find = %d record(s), %v; want the spent one and the refused one", len(found), err)
			}
			if own := recordOf(t, found, firstApproval); own.Resolution != approvals.ResolutionConsumed {
				t.Errorf("the plane's own record reads %v, want consumed", own.Resolution)
			}
			expectUnknown(t, recordOf(t, found, sibling), "req-1")
		})
	}
}

// TestFindTellsARefusedRecordFromAnUnreadableOne: a record that will not
// decode still refuses the whole Find, which is what the claim rule's
// exception is an exception to.
func TestFindTellsARefusedRecordFromAnUnreadableOne(t *testing.T) {
	p, dir, h := heldOne(t)
	answerAt(t, dir, minted.Add(time.Minute))
	forgeSibling(t, dir, answeredName, sibling+".1-answered.rec", "req-2", "approver-2")
	writeFile(t, dir, sibling+".1-answered.rec", withApprovalField(t, readFile(t, dir, sibling+".1-answered.rec"),
		"decidedAt", mustJSON(t, minted.Add(-time.Hour).Format(time.RFC3339Nano))))
	if found, err := p.Find(t.Context(), h.Binding, minted.Add(2*time.Minute)); !errors.Is(err, approvals.ErrDecidedAt) {
		t.Errorf("Find beside a record decided before its request = %d record(s), %v; want ErrDecidedAt", len(found), err)
	}
}
