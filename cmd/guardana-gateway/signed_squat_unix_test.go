//go:build unix

package main

import (
	"strings"
	"testing"

	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/policykey"
)

// squatName is the name bob's rejection of the held call takes under label
// suffix, with the approval id as spell gives it.
func squatName(held gateway.Disposition, spell func(string) string, suffix string) string {
	return spell(held.Pending.ApprovalID) + "." + policykey.KeyID(signedTestPublic("bob")) + "-reject" + suffix + ".answer"
}

func asIs(s string) string { return s }

// TestASquattedRejectionNameNeverLetsAnApprovalRun: mallory, who may write
// the inbox and holds no key, files under the name bob's rejection takes,
// before bob answers, an envelope whose body names another approval, junk
// under a signature naming bob's key, or bob's name with the approval id in
// another case. Beside alice's approval each holds the call INDETERMINATE at
// every retry; with bob's rejection then filed under a fresh label, the call
// is rejected; alice's approval alone runs it once.
func TestASquattedRejectionNameNeverLetsAnApprovalRun(t *testing.T) {
	for name, plant := range map[string]func(t *testing.T, tr tree, held gateway.Disposition){
		"a body for another approval": func(t *testing.T, tr tree, held gateway.Disposition) {
			a := answerOf(held, "bob", approvals.DecisionReject)
			a.ApprovalID = "OTHER1"
			fileSigned(t, tr, squatName(held, asIs, ""), "mallory", "bob", a)
		},
		"junk under bob's key id": func(t *testing.T, tr tree, held gateway.Disposition) {
			fileEnvelope(t, tr, squatName(held, asIs, ""), "mallory", "bob", []byte("junk"))
		},
		"the approval id in another case": func(t *testing.T, tr tree, held gateway.Disposition) {
			if strings.ToLower(held.Pending.ApprovalID) == held.Pending.ApprovalID {
				t.Fatalf("approval id %s has no letter to fold", held.Pending.ApprovalID)
			}
			fileSigned(t, tr, squatName(held, strings.ToLower, ""), "mallory", "bob", answerOf(held, "bob", approvals.DecisionReject))
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr, p := signedHoldingPlaneOf(t, "alice", "bob")
			held := admitTransfer(t, p, "req-hold-1")
			if held.Action != core.AwaitApproval {
				t.Fatalf("the first call is %v, want a hold", held.Action)
			}
			plant(t, tr, held)
			fileAnswer(t, tr, held, aliceLabel(0))
			for _, id := range []string{"req-hold-2", "req-hold-3"} {
				d := admitTransfer(t, p, id)
				if d.Action != core.Block || d.Decision.GetVerdict().String() != "VERDICT_INDETERMINATE" {
					t.Fatalf("%s is %v %s %v; want INDETERMINATE", id, d.Action, d.Decision.GetVerdict(), d.Decision.GetReasonCodes())
				}
			}
			fileSigned(t, tr, squatName(held, asIs, "-2"), "bob", "bob", answerOf(held, "bob", approvals.DecisionReject))
			d := admitTransfer(t, p, "req-hold-4")
			if d.Action != core.Block || d.Decision.GetVerdict().String() != "VERDICT_DENY" || !hasCode(d, "APPROVAL_REJECTED") {
				t.Fatalf("after bob's rejection the retry is %v %s %v; want it rejected", d.Action, d.Decision.GetVerdict(), d.Decision.GetReasonCodes())
			}
		})
	}
	t.Run("alice's approval alone", func(t *testing.T) {
		tr, p := signedHoldingPlaneOf(t, "alice", "bob")
		held := admitTransfer(t, p, "req-hold-1")
		fileAnswer(t, tr, held, aliceLabel(0))
		d := admitTransfer(t, p, "req-hold-2")
		if d.Action != core.Execute {
			t.Fatalf("the retry is %v %v", d.Action, d.Decision.GetReasonCodes())
		}
		closeExecution(t, p, d)
		if again := admitTransfer(t, p, "req-hold-3"); again.Action == core.Execute {
			t.Fatal("the approval ran a second call")
		}
	})
}

func hasCode(d gateway.Disposition, code string) bool {
	for _, c := range d.Decision.GetReasonCodes() {
		if c == code {
			return true
		}
	}
	return false
}
