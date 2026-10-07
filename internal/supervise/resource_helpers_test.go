package supervise_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
)

const siblingRun = "run-33333333333333333333333333333333"

// siblings is the root runID and two children of it, childRun and siblingRun.
func siblings() []supervise.Run {
	return []supervise.Run{
		{ID: runID, Tenant: tenant},
		{ID: childRun, Tenant: tenant, Parent: runID},
		{ID: siblingRun, Tenant: tenant, Parent: runID},
	}
}

// act is a call whose envelope also names a resource, an arguments hash and
// an effect. An act with an approval is held, then decided by the approvals
// store: run when approved, blocked when rejected.
type act struct {
	call
	res      *controlv1.Resource
	hash     string
	effect   controlv1.EffectClass
	approval controlv1.ApprovalState
}

// orderNo is the shop order id in the tenant's production environment.
func orderNo(id string) *controlv1.Resource {
	return &controlv1.Resource{Type: "shop_order", Id: id, TenantId: tenant, Environment: "prod"}
}

func hashOf(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }

func (a act) events() []*controlv1.Event {
	c := a.call
	if a.approval != controlv1.ApprovalState_APPROVAL_STATE_UNSPECIFIED {
		c.outcome = "held"
	}
	evs := c.events()
	env := evs[0].GetProposed()
	env.Resource, env.Action.Effect = a.res, a.effect
	if a.hash != "" {
		env.Arguments = &controlv1.Arguments{CanonicalHash: a.hash}
	}
	if a.approval == controlv1.ApprovalState_APPROVAL_STATE_UNSPECIFIED {
		return evs
	}
	type step struct {
		kind    controlv1.EventKind
		payload func(*controlv1.Event)
	}
	tail := []step{{controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED, func(ev *controlv1.Event) {
		ev.Payload = &controlv1.Event_Approval{Approval: &controlv1.Approval{SchemaVersion: "1.0", ApprovalId: c.req + "-a",
			RequestId: c.req, State: a.approval, ApproverId: "ops"}}
	}}}
	if a.approval == controlv1.ApprovalState_APPROVAL_STATE_APPROVED {
		tail = append(tail, step{controlv1.EventKind_EVENT_KIND_ACTION_STARTED, func(ev *controlv1.Event) { ev.ExecutionId = c.req + "-x" }},
			step{controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED, func(ev *controlv1.Event) { ev.ExecutionId = c.req + "-x" }})
	} else {
		tail = append(tail, step{controlv1.EventKind_EVENT_KIND_ACTION_BLOCKED, func(ev *controlv1.Event) {
			ev.Payload = &controlv1.Event_Decision{Decision: decision(c.req+"-p", c.req, controlv1.Verdict_VERDICT_DENY, "APPROVAL_REJECTED")}
		}})
	}
	for _, step := range tail {
		i := len(evs)
		ev := &controlv1.Event{
			EventId: fmt.Sprintf("%s-e%d", c.req, i+1), Kind: step.kind, RequestId: c.req, RunId: pick(c.run, runID),
			ProjectId: proj, TenantId: tenant, OccurredAt: at(c.at + time.Duration(i)*time.Second), SchemaVersion: "1.0",
			EnforcementMode: controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE, PrevEventId: evs[i-1].GetEventId(),
		}
		step.payload(ev)
		evs = append(evs, ev)
	}
	return evs
}

// acts is one whole export of the acts' trails, in order.
func acts(as ...act) supervise.Export {
	x := supervise.Export{Whole: true}
	for _, a := range as {
		x.Events = append(x.Events, a.events()...)
	}
	return x
}

// lookupOf and refundOf are calls of the two tools that bind order.
func lookupOf(req string, d time.Duration, res *controlv1.Resource) act {
	return act{call: call{req: req, tool: "get_order", upstream: "shop", at: d}, res: res}
}

func refundOf(req string, d time.Duration, res *controlv1.Resource) act {
	return act{call: call{req: req, tool: "issue_refund", upstream: "pay", at: d}, res: res}
}

func (a act) in(run string) act         { a.run = run; return a }
func (a act) ending(outcome string) act { a.outcome = outcome; return a }

// supervise02 evaluates exports under the inherit fixture over tree.
func supervise02(t *testing.T, tree []supervise.Run, exports ...supervise.Export) *supervise.Result {
	t.Helper()
	return treeEvaluate(t, supervise.Input{Procedure: inheritProc(t), Tree: tree, Exports: exports})
}

// verdicts is each finding of rule as its run and verdict, in order.
func verdicts(res *supervise.Result, rule string) []string {
	var out []string
	for _, f := range only(res, rule) {
		out = append(out, f.GetFinding().GetRunId()+" "+f.GetFinding().GetVerdict().String())
	}
	return out
}

// citedEvents is the event ids a finding cites, in order.
func citedEvents(f *findingv1alpha1.FindingRecord) string {
	var out []string
	for _, r := range f.GetRefs() {
		if e := r.GetEvent(); e != nil {
			out = append(out, e.GetEventId())
		} else {
			out = append(out, r.GetObservation().GetObservationId())
		}
	}
	return strings.Join(out, " ")
}
