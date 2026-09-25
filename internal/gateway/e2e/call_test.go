package e2e_test

import (
	"slices"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

	"github.com/guardana/control/adapters/mcp"
)

// listeners is every agent-facing transport the adapter serves, with the
// revision each negotiates. The cases below that hold on every transport run
// once per row.
var listeners = []struct {
	name string
	kind mcp.Kind
}{
	{"stateless-2026-07-28", mcp.KindStatelessHTTP},
	{"stateful-2025-11-25", mcp.KindStatefulHTTP},
	{"stdio", mcp.KindStdio},
}

// TestAllowedCallRunsOnceWithTheAuthorizedBytes runs on every listener: the
// upstream ran once with exactly the bytes the decision authorized, the trail
// is PROPOSED, DECIDED, STARTED, COMPLETED and validates, the executed digest
// is the decision's, and the exporter delivered every record of it to the
// collector.
func TestAllowedCallRunsOnceWithTheAuthorizedBytes(t *testing.T) {
	for _, l := range listeners {
		t.Run(l.name, func(t *testing.T) {
			p := newPlane(t, options{kind: l.kind, mode: modeEnforce, rules: []string{allowReads}})
			agent := p.connect(t, "agent-a")
			res := allowed(t, agent, toolRead, map[string]any{"path": "/x"})
			if len(res.Content) == 0 {
				t.Errorf("the upstream's answer reached the agent empty: %+v", res)
			}
			if got := p.victim.args(toolRead); !slices.Equal(got, []string{`{"path":"/x"}`}) {
				t.Fatalf("the upstream received %v, want exactly one call with the agent's bytes", got)
			}
			trail := p.oneTrail(t)
			expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
			decision := eventOf(t, trail, kindDecided).GetDecision()
			if !slices.Contains(decision.GetReasonCodes(), codeRuleAllow) {
				t.Errorf("the recorded decision says %v", decision.GetReasonCodes())
			}
			result := eventOf(t, trail, kindCompleted).GetResult()
			if result.GetExecutedActionDigest() == "" || result.GetExecutedActionDigest() != decision.GetActionDigest() {
				t.Errorf("executed digest %q, decision's %q", result.GetExecutedActionDigest(), decision.GetActionDigest())
			}
			if result.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_SUCCESS {
				t.Errorf("the closing record says %v", result.GetStatus())
			}
			if result.GetExecutionId() == "" || result.GetExecutionId() != eventOf(t, trail, kindStarted).GetExecutionId() {
				t.Errorf("the closing record names execution %q, ACTION_STARTED %q",
					result.GetExecutionId(), eventOf(t, trail, kindStarted).GetExecutionId())
			}
			expectDelivered(t, p, trail)
		})
	}
}

// TestDeniedCallNeverReachesTheUpstream runs on every listener: the upstream
// never ran, the agent got an isError result carrying the decision's codes and
// id, and the trail ends in BLOCKED.
func TestDeniedCallNeverReachesTheUpstream(t *testing.T) {
	for _, l := range listeners {
		t.Run(l.name, func(t *testing.T) {
			p := newPlane(t, options{kind: l.kind, mode: modeEnforce, rules: []string{denyDeletes}})
			agent := p.connect(t, "agent-a")
			res := call(t, agent, toolDelete, map[string]any{"path": "/x"})
			blockedWith(t, res, codeRuleDeny)
			if n := p.victim.ran(); n != 0 {
				t.Fatalf("the upstream ran %d times for a denied call", n)
			}
			trail := p.oneTrail(t)
			expectTrail(t, trail, kindProposed, kindDecided, kindBlocked)
			decision := eventOf(t, trail, kindBlocked).GetDecision()
			if structured(t, res)["decision_id"] != decision.GetDecisionId() {
				t.Errorf("the agent was told decision %v, the trail records %q",
					structured(t, res)["decision_id"], decision.GetDecisionId())
			}
			if decision.GetVerdict() != controlv1.Verdict_VERDICT_DENY {
				t.Errorf("the blocking decision is %v", decision.GetVerdict())
			}
			if s := p.pipeline.Stats(); s.Executed != 0 || s.Blocks[codeRuleDeny] != 1 {
				t.Errorf("stats %+v", s)
			}
		})
	}
}

// TestRewritingObligationReachesTheWire: the capped amount and the removed
// field are what the upstream received, and the recorded decision is the one
// about those bytes, which the executed digest equals.
func TestRewritingObligationReachesTheWire(t *testing.T) {
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{cappedTransfers}})
	agent := p.connect(t, "agent-a")
	res := allowed(t, agent, toolTransfer, map[string]any{"amount": 5000, "account": "a1", "ssn": "x"})
	if res.IsError {
		t.Fatalf("the call was refused: %+v", res.StructuredContent)
	}
	got := p.victim.args(toolTransfer)
	if !slices.Equal(got, []string{`{"account":"a1","amount":1000}`}) {
		t.Fatalf("the upstream received %v, want the capped amount and no ssn", got)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
	proposed := eventOf(t, trail, kindProposed).GetProposed()
	decision := eventOf(t, trail, kindDecided).GetDecision()
	result := eventOf(t, trail, kindCompleted).GetResult()
	if !slices.Contains(decision.GetReasonCodes(), codeObligations) {
		t.Errorf("the recorded decision says %v", decision.GetReasonCodes())
	}
	// The authorized action, not the proposed one: the recorded decision is
	// about the bytes that ran, and the executed digest is the same action
	// digest (ADR-0013).
	if decision.GetActionDigest() == "" || decision.GetActionDigest() != result.GetExecutedActionDigest() {
		t.Errorf("decision digest %q, executed %q", decision.GetActionDigest(), result.GetExecutedActionDigest())
	}
	if proposed.GetArguments().GetCanonicalHash() == "" {
		t.Fatal("the proposed envelope names no arguments hash")
	}
	if hash := argumentsHash(t, `{"account":"a1","amount":1000}`); proposed.GetArguments().GetCanonicalHash() == hash {
		t.Error("the proposed record carries the rewritten bytes' hash; it has to keep what the agent asked for")
	}
}

// TestUnclassifiedToolIsBlockedOutsideObserve: a tool no override classifies is
// blocked under ENFORCE, and the upstream never sees it.
func TestUnclassifiedToolIsBlockedOutsideObserve(t *testing.T) {
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes}})
	agent := p.connect(t, "agent-a")
	res := call(t, agent, toolUnlisted, map[string]any{"path": "/x"})
	blockedWith(t, res, codeUnclassified)
	if n := p.victim.count(toolUnlisted); n != 0 {
		t.Fatalf("the upstream ran the unclassified tool %d times", n)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindBlocked)
	if pdp := eventOf(t, trail, kindBlocked).GetDecision().GetPdpType(); pdp != gatewayPDP {
		t.Errorf("the block names pdp_type %q, want the enforcement point's %q", pdp, gatewayPDP)
	}
}

// TestUnclassifiedToolRunsUnderObserve: OBSERVE is how an operator learns
// which tools exist, so the call runs and is recorded.
func TestUnclassifiedToolRunsUnderObserve(t *testing.T) {
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeObserve, rules: []string{denyDeletes}})
	agent := p.connect(t, "agent-a")
	allowed(t, agent, toolUnlisted, map[string]any{"path": "/x"})
	if got := p.victim.args(toolUnlisted); !slices.Equal(got, []string{`{"path":"/x"}`}) {
		t.Fatalf("the upstream received %v", got)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
	if mode := trail[0].GetEnforcementMode(); mode != modeObserve {
		t.Errorf("the records say mode %v", mode)
	}
}

// expectDelivered waits for the exporter to deliver every record of the trail
// to the collector, which is the proof that the spool and the exporter carried
// the evidence and not only that the pipeline wrote it.
func expectDelivered(t *testing.T, p *plane, trail []*controlv1.Event) {
	t.Helper()
	eventually(t, func() bool { return delivered(p, trail) })
}
