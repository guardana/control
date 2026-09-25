package gateway

import (
	"context"
	"errors"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/pkg/contract"
)

// planeState is what the plane's own causes to block a call are a function
// of: the call, the mode, the pause snapshot the call took, and the halts.
type planeState struct {
	mode         controlv1.EnforcementMode
	material     bool
	unclassified bool
	paused       bool
	pauseUnknown bool
	sinkHalt     bool
	mismatchHalt bool
}

// cause is one reason the plane blocks a call whatever the policy decides,
// with the verdict it carries.
type cause struct {
	code    string
	verdict controlv1.Verdict
}

// planeCauses names, in the order a decision lists them, every cause the
// plane has to block a call whatever the policy says: the DENY causes first,
// then the INDETERMINATE ones (ADR-0019). None means the policy decides.
func planeCauses(s planeState) []cause {
	var out []cause
	for _, c := range []struct {
		on bool
		cause
	}{
		{s.paused, cause{codePaused, verdictDeny}},
		{s.material && s.mismatchHalt, cause{codeExecutedArgsMismatch, verdictDeny}},
		{s.material && s.mode == modeLockdown, cause{codeLockdown, verdictDeny}},
		{s.pauseUnknown, cause{codePauseStateUnavailable, verdictIndeterminate}},
		{s.material && s.sinkHalt, cause{codeEvidenceUnavailable, verdictIndeterminate}},
		{s.unclassified && s.mode != modeObserve, cause{codeActionUnclassified, verdictIndeterminate}},
	} {
		if c.on {
			out = append(out, c.cause)
		}
	}
	return out
}

// verdictOf is DENY when any cause denies, and INDETERMINATE otherwise.
func verdictOf(causes []cause) controlv1.Verdict {
	for _, c := range causes {
		if c.verdict == verdictDeny {
			return verdictDeny
		}
	}
	return verdictIndeterminate
}

// newCall starts one admission under the pause state and the clock as it
// takes them now.
func (p *Pipeline) newCall(ctx context.Context, a Admission) *call {
	c := &call{p: p, ctx: ctx, in: a}
	c.takePause()
	return c
}

// takePause takes the pause state and then reads the clock the call judges
// its age by. The other order would let a poll published between the two
// date the state ahead of the call and block it as unreadable.
func (c *call) takePause() {
	snap := c.p.cfg.Pause.Current()
	c.now = c.p.cfg.Clock()
	c.pause = snap.At(c.now)
}

// pausedNow takes the pause state and the clock again right before an
// irreversible step, a resume consuming its approval or an execution handed
// out, since a store or a sink that syncs can outlast a snapshot. A state that
// covers the call or cannot be read blocks it there, listing every cause the
// plane names now, and reports so.
func (c *call) pausedNow() bool {
	c.takePause()
	state := c.planeState()
	if !state.paused && !state.pauseUnknown {
		return false
	}
	c.causes = planeCauses(state)
	c.blockOnCauses()
	return true
}

// planeState is this call's state as the plane sees it now: the halts as
// they stand, and the pause state the call took last.
func (c *call) planeState() planeState {
	sink, mismatch := c.p.counts.halts()
	action := c.in.Envelope.GetAction()
	state := c.pause.State()
	return planeState{
		mode:         c.p.cfg.Mode,
		material:     contract.IsMaterial(action.GetEffect()),
		unclassified: errors.Is(c.in.Refusal, ErrUnclassified),
		paused:       state == pause.Paused && c.pause.Covers(action.GetKind(), action.GetProvider(), action.GetName()),
		pauseUnknown: state == pause.Unknown,
		sinkHalt:     sink,
		mismatchHalt: mismatch,
	}
}
