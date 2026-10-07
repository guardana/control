package supervise

import (
	"slices"
	"time"

	findingv1alpha1 "github.com/guardana/control/api/gen/go/guardana/control/finding/v1alpha1"
	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/observe"
)

// Outcome is how a call ended as its trail says. The zero value is unknown:
// a trail in doubt, or a source's report, whose own claim is no outcome.
type Outcome int

// How a call ended.
const (
	OutcomeUnknown Outcome = iota
	// OutcomeOpen is a trail with no end yet, such as a call held for
	// approval.
	OutcomeOpen
	OutcomeCompleted
	OutcomeFailed
	// OutcomeDenied is a block on the policy's own DENY.
	OutcomeDenied
	// OutcomeBlocked is any other block: the plane's own, such as a pause, a
	// stop or an approval refused or expired, or the kernel's on a verdict
	// other than DENY.
	OutcomeBlocked
)

// Approval is what a call's trail says of an approval. The zero value
// claims none was granted.
type Approval int

// What a trail says of an approval.
const (
	// ApprovalNone is a trail that asks for no approval.
	ApprovalNone Approval = iota
	// ApprovalAsked is one asked for and not granted as far as a coherent
	// trail says: held, refused, expired, or granted in a trail in doubt.
	ApprovalAsked
	// ApprovalGranted is one the approvals store granted, in a coherent
	// trail.
	ApprovalGranted
)

// Resource is the resource a call's envelope names, compared as bytes.
type Resource struct {
	Type, ID, Tenant, Environment string
}

// Instance is one call the supervision judged, as plain data: a plane
// request, or a source's report of a tool call no plane request joins.
type Instance struct {
	// Request is the plane request's id, "" for a report.
	Request string
	// Observation and Source name a report, "" for a plane request. Trust is
	// the trust its source declared.
	Observation, Source string
	Trust               observev1.Trust
	// Tool and Upstream are what the call proposed; a report names a tool
	// and no upstream.
	Tool, Upstream string
	// Step is the id of the step the call is, "" when it is none.
	Step string
	Run  string
	// At is when the call was proposed, or reported, when Timed. Told is
	// true for a plane call with a time no call of another export shares, so
	// the planes' clocks order it against every call with a time; a report's
	// time is its source's clock and never is.
	At          time.Time
	Timed, Told bool
	// Export is the place in Input.Exports of the call's proposal, -1 for a
	// report.
	Export   int
	Outcome  Outcome
	Approval Approval
	Resource Resource
	// Mode is the plane's own mode on the event that recorded its decision,
	// unspecified when no decision was read.
	Mode controlv1.EnforcementMode
	// Reasons are the reason codes of the decision that blocked the call,
	// else of the kernel's decision.
	Reasons []string
	// Doubt is true when the trail is not one coherent chain, or the report
	// can suggest nothing.
	Doubt bool
	// Excepted is true when an exception the procedure states was taken on a
	// finding that cites the call.
	Excepted bool
}

// instancesOf is every call judged, and for each finding the places of the
// calls it cites, each once. A call an EXCEPTION_TAKEN cites is Excepted.
func (e *evaluation) instancesOf(findings []*findingv1alpha1.FindingRecord) ([]Instance, [][]int) {
	out := make([]Instance, 0, len(e.reqs)+len(e.unjoined))
	byRequest, byObservation := map[string]int{}, map[string]int{}
	for _, rq := range e.reqs {
		if tool, ok := rq.tool(); ok {
			byRequest[rq.id] = len(out)
			out = append(out, e.callOf(rq, tool))
		}
	}
	tell(out)
	for _, o := range e.unjoined {
		byObservation[o.GetObservationId()] = len(out)
		out = append(out, e.reportOf(o))
	}
	rests := make([][]int, len(findings))
	for i, f := range findings {
		for _, r := range f.GetRefs() {
			n, ok := byRequest[r.GetEvent().GetRequestId()]
			if o := r.GetObservation(); o != nil {
				n, ok = byObservation[o.GetObservationId()]
			} else if r.GetEvent() == nil {
				ok = false
			}
			if ok && !slices.Contains(rests[i], n) {
				rests[i] = append(rests[i], n)
			}
		}
		if f.GetFinding().GetRuleId() == RuleExceptionTaken {
			for _, n := range rests[i] {
				out[n].Excepted = true
			}
		}
	}
	return out, rests
}

func (e *evaluation) callOf(rq *request, tool [2]string) Instance {
	p := rq.proposal
	in := Instance{Request: rq.id, Tool: tool[0], Upstream: tool[1], Run: p.GetRunId(), Export: e.rd.exportOf[p],
		Timed: p.GetOccurredAt().IsValid(), Outcome: outcomeOf(rq), Approval: approvalOf(rq),
		Mode: rq.decided.GetEnforcementMode(), Doubt: rq.doubt}
	if in.Timed {
		in.At = p.GetOccurredAt().AsTime()
	}
	if en, ok := e.ix.byTool[tool]; ok && en.step != noStep {
		in.Step = e.p.steps[en.step].ID
	}
	if r := p.GetProposed().GetResource(); r != nil {
		in.Resource = Resource{Type: r.GetType(), ID: r.GetId(), Tenant: r.GetTenantId(), Environment: r.GetEnvironment()}
	}
	codes := rq.kernel.GetReasonCodes()
	if rq.terminal.GetKind() == blocked {
		codes = rq.terminal.GetDecision().GetReasonCodes()
	}
	in.Reasons = slices.Clone(codes)
	return in
}

func outcomeOf(rq *request) Outcome {
	switch {
	case rq.doubt:
		return OutcomeUnknown
	case rq.terminal == nil:
		return OutcomeOpen
	case rq.denied():
		return OutcomeDenied
	}
	switch rq.terminal.GetKind() {
	case controlv1.EventKind_EVENT_KIND_ACTION_COMPLETED:
		return OutcomeCompleted
	case controlv1.EventKind_EVENT_KIND_ACTION_FAILED:
		return OutcomeFailed
	case blocked:
		return OutcomeBlocked
	}
	return OutcomeUnknown
}

func approvalOf(rq *request) Approval {
	if rq.approval == controlv1.ApprovalState_APPROVAL_STATE_APPROVED && !rq.doubt {
		return ApprovalGranted
	}
	for _, ev := range rq.events {
		switch ev.GetKind() {
		case controlv1.EventKind_EVENT_KIND_APPROVAL_REQUESTED, controlv1.EventKind_EVENT_KIND_APPROVAL_DECIDED,
			controlv1.EventKind_EVENT_KIND_APPROVAL_EXPIRED:
			return ApprovalAsked
		}
	}
	return ApprovalNone
}

// tell marks told each plane call with a time that no call of another
// export shares.
func tell(ins []Instance) {
	type instant struct {
		sec  int64
		nsec int
	}
	const shared = -2
	exports := map[instant]int{}
	for _, in := range ins {
		if !in.Timed {
			continue
		}
		k := instant{in.At.Unix(), in.At.Nanosecond()}
		if x, seen := exports[k]; !seen {
			exports[k] = in.Export
		} else if x != in.Export {
			exports[k] = shared
		}
	}
	for i := range ins {
		ins[i].Told = ins[i].Timed && exports[instant{ins[i].At.Unix(), ins[i].At.Nanosecond()}] != shared
	}
}

// reportOf is a source's report of a tool call no plane call joins.
func (e *evaluation) reportOf(o *observev1.Observation) Instance {
	id := o.GetObservationId()
	in := Instance{Observation: id, Source: e.rd.obsSource[id].SourceID, Trust: observe.TrustOf(o.GetSource().GetTrust()),
		Tool: o.GetSubject().GetName(), Run: o.GetCorrelation().GetRunId(), Export: -1,
		Timed: o.GetEventTime().IsValid(), Doubt: e.obsVerdict(o) == indeterminate}
	if in.Timed {
		in.At = o.GetEventTime().AsTime()
	}
	if en, ok := e.ix.byName[in.Tool]; ok && en.step != noStep {
		in.Step = e.p.steps[en.step].ID
	}
	return in
}
