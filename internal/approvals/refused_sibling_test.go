package approvals_test

import (
	"errors"
	"testing"
	"time"

	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core/approval"
)

// TestARefusedRecordOfAnotherApprovalBlocksNothing: a writer of the directory
// files a second record for the same request and binding, under another
// approval id and an approver id the claim rule refuses. It refuses a call
// about its own approval and nothing else: the plane's own approval is found
// and spent as if the sibling were not there.
func TestARefusedRecordOfAnotherApprovalBlocksNothing(t *testing.T) {
	const sibling = "APPROVAL2"
	p, dir, h := heldOne(t)
	answerAt(t, dir, minted.Add(time.Minute))
	forged := withField(t, readFile(t, dir, answeredName), "approval_id", `"`+sibling+`"`)
	forged = withApprovalField(t, forged, "approvalId", `"`+sibling+`"`)
	forged = withApprovalField(t, forged, "approverId", `"authn:okta:alice"`)
	writeFile(t, dir, sibling+".1-answered.rec", forged)
	now := minted.Add(2 * time.Minute)

	found, err := p.Find(t.Context(), h.Binding, now)
	if err != nil || len(found) != 1 || found[0].ApprovalID != firstApproval {
		t.Fatalf("Find = %v, %v; want the plane's own record alone", found, err)
	}
	if got, err := p.Consume(t.Context(), h.Binding, "req-1", sibling, now); got != nil || !errors.Is(err, approval.ErrReservedApproverID) {
		t.Errorf("Consume of the refused record = %v, %v; want nothing and ErrReservedApproverID", got, err)
	}
	got, err := p.Consume(t.Context(), h.Binding, "req-1", firstApproval, now)
	if err != nil || got.GetApproverId() != "approver-1" {
		t.Fatalf("Consume of the plane's own record = %v, %v; want it spent", got, err)
	}
	assertNames(t, dir, consumedName, sibling+".1-answered.rec")
	found, err = p.Find(t.Context(), h.Binding, now)
	if err != nil || len(found) != 1 || found[0].Resolution != approvals.ResolutionConsumed {
		t.Errorf("Find after the consume = %v, %v; want the spent record alone", found, err)
	}
}
