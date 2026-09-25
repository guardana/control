package gateway

import (
	"context"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/pkg/contract"
)

// call is one admission on its way through the pipeline: what the kernel
// said, what the mode made of it, and the trail it writes.
type call struct {
	p   *Pipeline
	ctx context.Context
	in  Admission
	now time.Time
	// pause is the operator's pause state as this call took it: once at
	// admission and, when the decision point is asked, once more after the
	// ask, at the clock read after it. Every decision after the ask, the
	// second one on the rewrite path included, is made under that one.
	pause pause.Snapshot
	// causes are the plane's own causes to block this call, named once
	// before any ask and, after one, once more. Whatever the mode does is
	// done under these; nothing names them again.
	causes []cause
	// flow is the run's state as this call took it when it started; every
	// decision of the call is made under it, after an ask included.
	flow flow
	// forged: the producer sent a tag under the reserved flow prefix.
	forged bool

	// kernel is the decision POLICY_DECIDED records: the kernel's about the
	// proposed envelope, or about the authorized one when a rewrite changed
	// the arguments.
	kernel   *controlv1.Decision
	action   core.EnforcementAction
	decision *controlv1.Decision
	material bool
	// external is the decision point's answer about this call, asked at
	// most once; the zero value when it was not asked.
	external core.External
	// args, digest and rest are the authorized arguments, their arguments
	// hash and the obligations left for the adapter; env is the authorized
	// envelope and actionDigest its canonical action digest with args.
	args         []byte
	digest       string
	rest         []*controlv1.Obligation
	env          *controlv1.ActionEnvelope
	actionDigest string

	trail *evidence.Builder
	// requestID is the trail this call writes on and owns while it is open:
	// its own, or the held request's when it resumes one.
	requestID string
	// unrecorded: the sink refused an event of this trail and the read goes
	// on under AllowReadsUnrecorded.
	unrecorded bool
	// held: the trail stands past a request for approval, so none of its
	// records goes unrecorded, and one the sink refuses leaves the trail open
	// with its journal entry and its request id.
	held bool
}

func (c *call) run() Disposition {
	snap := c.p.cfg.Policy.Current()
	c.takeFlow()
	// A call the plane blocks whatever the answer is not asked about: its
	// question would leave the plane for nothing, during the very incident a
	// pause or a halt stands for (ADR-0019).
	c.causes = planeCauses(c.planeState())
	c.decideProposed(snap, len(c.causes) == 0)

	trail, err := c.newTrail()
	if err != nil {
		// Nothing can be written about this request, so nothing runs: the
		// kernel's own refusal blocks it, and a request the kernel took but
		// the trail cannot name is refused on its identifiers.
		if c.action != core.Block {
			c.decide(verdictIndeterminate, codeInvalidFieldValue)
		}
		c.p.counts.blocked(firstCode(c.decision))
		return Disposition{Action: core.Block, Decision: c.decision}
	}
	c.trail = trail

	c.applyMode()
	c.refuseFlow()
	if c.action != core.Block {
		c.authorize(snap)
	}
	if c.action == core.AwaitApproval {
		return c.approve(snap)
	}
	if !c.openTrail() {
		return c.refuseOpen()
	}
	if !c.record(c.trail.Proposed(c.in.Envelope)) || !c.record(c.trail.Decided(c.kernel)) {
		return c.blockUnrecorded()
	}
	if c.action == core.Block {
		d, _ := c.block()
		return d
	}
	return c.start(c.trail)
}

// decideProposed asks the kernel about the proposed envelope and bytes. When
// ask is set and the kernel reports that its decision turns on the decision
// point's answer, and that decision is not a DENY no answer can lift, it asks
// the decision point once and decides again with the answer, in every mode.
// After the ask the call takes the pause state and the clock again and names
// the plane's causes once more, since a pause, a halt or an expiry can come
// while the ask waits.
func (c *call) decideProposed(snap *policy.Snapshot, ask bool) {
	req := core.Request{
		Envelope: c.in.Envelope, Refusal: c.in.Refusal, AuthorizedArgs: c.in.Arguments, Flow: c.flow.state,
	}
	out := c.p.kernel.Decide(c.ctx, req, snap)
	if ask && out.NeedsExternal && out.Decision.GetVerdict() != verdictDeny {
		c.external = c.p.ask(c.ctx, c.in.Envelope)
		c.takePause()
		c.causes = planeCauses(c.planeState())
		req.External = c.external
		out = c.p.kernel.Decide(c.ctx, req, snap)
	}
	c.kernel, c.decision, c.action = out.Decision, out.Decision, out.Action
	c.material = contract.IsMaterial(c.in.Envelope.GetAction().GetEffect())
}

// newTrail builds the trail from the envelope's identifiers, timed at this
// call's clock reading.
func (c *call) newTrail() (*evidence.Builder, error) {
	return evidence.NewBuilder(c.trailIDs(), c.p.cfg.Mode, func() time.Time { return c.now }, c.p.cfg.NewID)
}

// trailIDs names this call's trail from the envelope's identifiers and the
// run the pipeline minted, never the run id the producer sent.
func (c *call) trailIDs() evidence.IDs {
	env := c.in.Envelope
	return evidence.IDs{
		RequestID: env.GetRequestId(),
		RunID:     c.flow.runID(),
		ProjectID: env.GetProjectId(),
		TenantID:  env.GetTenantId(),
	}
}

// applyMode applies the plane's causes and mode to the action and never to
// the kernel's decision (ADR-0013). A call the plane named a cause of its own
// to block is blocked whatever the mode, OBSERVE included, with every such
// cause listed; otherwise OBSERVE lets the call through and APPROVE holds an
// allowed material one.
func (c *call) applyMode() {
	if len(c.causes) > 0 {
		c.blockOnCauses()
		return
	}
	switch c.p.cfg.Mode {
	case modeObserve:
		c.action = core.Execute
	case modeApprove:
		if c.material && (c.action == core.Execute || c.action == core.ExecuteWithObligations) {
			c.action = core.AwaitApproval
		}
	}
}

// blockOnCauses blocks the call with the plane's decision, which lists every
// cause named in order.
func (c *call) blockOnCauses() {
	codes := make([]string, len(c.causes))
	for i, cause := range c.causes {
		codes[i] = cause.code
	}
	c.decide(verdictOf(c.causes), codes...)
}

// decide replaces the action with a block and the decision with the plane's
// own; the kernel's stays what POLICY_DECIDED records.
func (c *call) decide(verdict controlv1.Verdict, codes ...string) {
	c.action = core.Block
	c.decision = c.p.mint(c.kernel, c.now, verdict, codes...)
}

// openTrail opens this call's own trail, or reports that its request id has
// a trail open in this pipeline already: two trails under one id would
// interleave.
func (c *call) openTrail() bool {
	id := c.in.Envelope.GetRequestId()
	if c.requestID == id {
		return true
	}
	if !c.p.reserve(c.ctx, id, c.now) {
		return false
	}
	c.requestID = id
	return true
}

// refuseOpen blocks a call whose request id has an open trail, writing
// nothing: the only trail it could write on is another call's.
func (c *call) refuseOpen() Disposition {
	c.decide(verdictIndeterminate, codeInvalidFieldValue)
	c.p.counts.blocked(codeInvalidFieldValue)
	return Disposition{Action: core.Block, Decision: c.decision}
}

// record appends event and reports whether the call may go on writing. A
// refused append blocks a material call, a call on a held trail and, without
// the risk setting, a read; under AllowReadsUnrecorded any other read goes on
// unrecorded.
func (c *call) record(event *controlv1.Event) bool {
	if c.unrecorded {
		return true
	}
	gen := c.p.counts.generation()
	if err := c.p.cfg.Sink.Append(c.ctx, event); err != nil {
		c.p.counts.sinkFailed(false)
		if c.material || c.held || !c.p.cfg.AllowReadsUnrecorded {
			return false
		}
		c.unrecorded = true
		c.p.counts.add(func(s *Stats) *uint64 { return &s.ReadsUnrecorded })
		return true
	}
	c.p.counts.appended(gen)
	return true
}

// blockUnrecorded is the block for a record the sink would not take: the one
// record that could not be written is the record of this block, so none is
// attempted. A held trail it leaves open keeps its request id reserved, and
// its journal entry where it has one, so no later call writes a second trail
// under that id or forgets that entry at its Close.
func (c *call) blockUnrecorded() Disposition {
	c.decide(verdictIndeterminate, codeEvidenceUnavailable)
	c.p.counts.blocked(codeEvidenceUnavailable)
	if c.held {
		c.p.counts.leftOpen()
	} else {
		c.close()
	}
	return Disposition{Action: core.Block, Decision: c.decision}
}

// block writes ACTION_BLOCKED with the decision the block carries, the
// kernel's or the plane's own, answers it, and reports whether the record is
// on the trail. A refused record the call may not go on without leaves the
// trail open, so the answer is blockUnrecorded's.
func (c *call) block() (Disposition, bool) {
	if !c.record(c.trail.Blocked(c.decision)) {
		return c.blockUnrecorded(), false
	}
	c.p.counts.blocked(firstCode(c.decision))
	c.close()
	return Disposition{Action: core.Block, Decision: c.decision}, !c.unrecorded
}

// close ends the trail this call owns.
func (c *call) close() {
	if c.requestID != "" {
		c.p.release(c.requestID)
		c.requestID = ""
	}
}

// start writes ACTION_STARTED on trail with a fresh execution id, keeps the
// execution for Close and hands out what to send. An execution a pause taken
// just now covers or cannot vouch for, or one the pipeline cannot name, cannot
// keep under its own name, or cannot keep within MaxOpen, is blocked before it
// is recorded as started; that block closes the trail, so a resumed hold's
// entry is forgotten there, as Close would.
func (c *call) start(trail *evidence.Builder) Disposition {
	requestID := c.requestID
	if c.pausedNow() {
		c.trail = trail
		d, closed := c.block()
		if closed {
			c.p.journalForget(c.ctx, requestID)
		}
		return d
	}
	handle, executionID := c.decision.GetDecisionId(), c.p.cfg.NewID()
	ex := &execution{
		trail: trail, requestID: requestID, executionID: executionID,
		envelope: c.env, digest: c.actionDigest, unrecorded: c.unrecorded,
	}
	if executionID == "" || !c.p.keep(handle, ex) {
		c.trail = trail
		c.decide(verdictIndeterminate, codeEvidenceUnavailable)
		d, closed := c.block()
		if closed {
			c.p.journalForget(c.ctx, requestID)
		}
		return d
	}
	if !c.record(trail.Started(executionID)) {
		c.p.drop(handle)
		return c.blockUnrecorded()
	}
	ex.unrecorded = c.unrecorded
	c.p.took(c.flow, c.in.ResultTrust, c.in.ResultSensitivity)
	c.requestID = ""
	c.p.counts.add(func(s *Stats) *uint64 { return &s.Executed })
	return Disposition{
		Action:           c.action,
		Decision:         c.decision,
		AuthorizedArgs:   c.args,
		AuthorizedDigest: c.digest,
		ExecutionID:      executionID,
		Obligations:      c.rest,
		handle:           handle,
	}
}

func firstCode(d *controlv1.Decision) string {
	if codes := d.GetReasonCodes(); len(codes) > 0 {
		return codes[0]
	}
	return ""
}
