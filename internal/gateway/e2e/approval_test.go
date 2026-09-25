package e2e_test

import (
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestPendingApprovalAndItsRetry runs on both HTTP revisions: the pending
// payload reaches the agent and the upstream does not run, a retry before the
// answer is the same pending state, the approval granted out of band lets the
// retry execute exactly once on the held trail, and the next identical call is
// APPROVAL_ALREADY_USED.
func TestPendingApprovalAndItsRetry(t *testing.T) {
	for _, l := range listeners {
		if l.kind == mcp.KindStdio {
			continue
		}
		t.Run(l.name, func(t *testing.T) {
			p := newPlane(t, options{kind: l.kind, mode: modeEnforce, rules: []string{approveTransfers}})
			agent := p.connect(t, "agent-a")
			args := map[string]any{"account": "a1", "amount": 250}
			requestID, approvalID, digest := expectHeld(t, p, agent, args)

			// The approver answers out of band, as a provider in the same
			// process would.
			if err := p.approvals.Answer(approvalID, controlv1.ApprovalState_APPROVAL_STATE_APPROVED,
				"approver-1", "ok", time.Now()); err != nil {
				t.Fatalf("Answer: %v", err)
			}

			granted := allowed(t, agent, toolTransfer, args)
			if granted.IsError {
				t.Fatalf("the approved retry was refused: %+v", granted.StructuredContent)
			}
			expectResumed(t, p, requestID, digest)

			// The next identical call finds the approval spent.
			used := call(t, agent, toolTransfer, args)
			blockedWith(t, used, codeApprovalAlreadyUsed)
			if n := p.victim.count(toolTransfer); n != 1 {
				t.Fatalf("the upstream ran %d times; an approval is consumed once", n)
			}
			order, byRequest := p.trails()
			if len(order) != 2 {
				t.Fatalf("trails after the spent retry: %v", order)
			}
			expectTrail(t, byRequest[order[1]], kindProposed, kindDecided, kindBlocked)
			if s := p.pipeline.Stats(); s.Executed != 1 || s.Pending != 2 || s.Blocks[codeApprovalAlreadyUsed] != 1 {
				t.Errorf("stats %+v", s)
			}
		})
	}
}

// expectHeld makes the first call and one retry before anybody answered: both
// are the same pending state on one trail, and the upstream never runs.
func expectHeld(t *testing.T, p *plane, agent *sdk.ClientSession, args map[string]any) (requestID, approvalID, digest string) {
	t.Helper()
	first := call(t, agent, toolTransfer, args)
	approvalID, digest = expectPending(t, first)
	if n := p.victim.ran(); n != 0 {
		t.Fatalf("the upstream ran %d times for a call awaiting approval", n)
	}
	held := p.oneTrail(t)
	expectTrail(t, held, kindProposed, kindDecided, kindRequested)
	requestID = held[0].GetRequestId()
	approval := eventOf(t, held, kindRequested).GetApproval()
	if approval.GetApprovalId() != approvalID || approval.GetActionDigest() != digest {
		t.Errorf("the agent was told %s/%s, the trail holds %s/%s",
			approvalID, digest, approval.GetApprovalId(), approval.GetActionDigest())
	}
	if approval.GetState() != controlv1.ApprovalState_APPROVAL_STATE_PENDING {
		t.Errorf("the held approval is %v", approval.GetState())
	}
	// A retry before anybody answered is the same held request, told to wait
	// again: no second trail and no second approval.
	retry := call(t, agent, toolTransfer, args)
	if again, _ := expectPending(t, retry); again != approvalID {
		t.Errorf("the retry was told approval %s, want the held %s", again, approvalID)
	}
	if order, _ := p.trails(); !slices.Equal(order, []string{requestID}) {
		t.Fatalf("a retry of the held request opened trails %v", order)
	}
	return requestID, approvalID, digest
}

// expectResumed checks that the approved retry ran exactly once on the held
// trail: no second trail, the answer recorded on it, and the execution on the
// digest the agent was told about.
func expectResumed(t *testing.T, p *plane, requestID, digest string) {
	t.Helper()
	if got := p.victim.args(toolTransfer); !slices.Equal(got, []string{`{"account":"a1","amount":250}`}) {
		t.Fatalf("the upstream received %v, want exactly one call", got)
	}
	order, byRequest := p.trails()
	if !slices.Equal(order, []string{requestID}) {
		t.Fatalf("the approved retry wrote trails %v, want the held request's alone", order)
	}
	resumed := byRequest[requestID]
	expectTrail(t, resumed, kindProposed, kindDecided, kindRequested, kindApproved, kindStarted, kindCompleted)
	decided := eventOf(t, resumed, kindApproved).GetApproval()
	if decided.GetApproverId() != "approver-1" || decided.GetState() != controlv1.ApprovalState_APPROVAL_STATE_APPROVED {
		t.Errorf("the recorded answer is %v by %q", decided.GetState(), decided.GetApproverId())
	}
	result := eventOf(t, resumed, kindCompleted).GetResult()
	if result.GetExecutedActionDigest() != eventOf(t, resumed, kindDecided).GetDecision().GetActionDigest() {
		t.Error("the execution ran on another digest than the trail's decision")
	}
	if digest != decided.GetActionDigest() {
		t.Errorf("the approval consumed names digest %s, the agent was told %s", decided.GetActionDigest(), digest)
	}
}

// expectPending reads the pending payload an agent has to be able to act on,
// and returns the approval id and the digest it names.
func expectPending(t *testing.T, res *sdk.CallToolResult) (approvalID, digest string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("a held call came back as a success: %+v", res)
	}
	sc := structured(t, res)
	if sc["reason_code"] != codeApprovalPending {
		t.Fatalf("the pending payload says %v", sc)
	}
	approvalID, _ = sc["approval_id"].(string)
	digest, _ = sc["action_digest"].(string)
	expires, _ := sc["expires_at"].(string)
	retry, hasRetry := sc["retry_after"]
	if approvalID == "" || digest == "" || expires == "" || !hasRetry {
		t.Fatalf("the pending payload is missing what a retry needs: %v", sc)
	}
	if _, err := time.Parse(time.RFC3339, expires); err != nil {
		t.Errorf("expires_at %q: %v", expires, err)
	}
	if seconds, ok := retry.(float64); !ok || seconds <= 0 {
		t.Errorf("retry_after is %v", retry)
	}
	return approvalID, digest
}
