package gateway

import (
	"context"
	"time"

	"github.com/guardana/control/pkg/contract"
)

// runStateWait bounds how long a raise waits for its root's lock, which
// another plane holds for one read and one synced write: a holder that hangs
// blocks the call rather than every call of the root for as long as it hangs.
var runStateWait = 5 * time.Second

// openedFlow takes the flow of a call on a plane that serves opened runs, or
// of a call that carries one: the run the listener resolved, whose identity
// has to be the envelope's, and its root's state, read once. Anything short
// of that is unavailable, which blocks the call in every mode. An unavailable
// flow still names the run once its identity was vouched for, so the trail of
// the call it blocks says whose it was.
func (p *Pipeline) openedFlow(ctx context.Context, in Admission) flow {
	f, ok := p.readOpened(ctx, in)
	if !ok {
		p.uncomputed()
		f.kind = flowUnavailable
	}
	return f
}

func (p *Pipeline) readOpened(ctx context.Context, in Admission) (flow, bool) {
	if p.cfg.Runs == nil || in.Run == nil || in.Run.ID == "" || in.Run.Root == "" {
		return flow{}, false
	}
	key, ok := keyOf(in.Envelope)
	if !ok || in.Run.Who != (RunIdentity{
		TenantID: key.tenant, PrincipalType: key.kind, PrincipalID: key.principal, AgentID: key.agent,
	}) {
		return flow{}, false
	}
	run := *in.Run
	read, err := p.cfg.Runs.State(ctx, in.Run.Root)
	if err != nil {
		p.counts.add(func(s *Stats) *uint64 { return &s.RunStateFailures })
		return flow{opened: &run}, false
	}
	return flow{
		kind: flowOpened, opened: &run, read: read,
		state: contract.NewFlowState(!read.Trusted, read.MaxRead),
	}, true
}

// raise writes into an opened run's root what the call's result brings in,
// before the execution is recorded as started, and reports whether the
// execution may go on. A result the state read when the call started already
// covers writes nothing, since a root's state only rises. Local runs are
// tainted in memory after ACTION_STARTED instead, as ADR-0021 has it.
func (c *call) raise() bool {
	if c.flow.kind != flowOpened {
		return true
	}
	join := func(s RunState) RunState {
		if contract.IsUntrusted(c.in.ResultTrust) {
			s.Trusted = false
		}
		s.MaxRead = joinRead(s.MaxRead, c.in.ResultSensitivity)
		return s
	}
	if join(c.flow.read) == c.flow.read {
		return true
	}
	ctx, cancel := context.WithTimeout(c.ctx, runStateWait)
	defer cancel()
	if err := c.p.cfg.Runs.Raise(ctx, c.flow.opened.Root, join); err != nil {
		c.p.counts.add(func(s *Stats) *uint64 { return &s.RunStateFailures })
		return false
	}
	return true
}

// runLapsed reports whether the call's opened run expired by the clock the
// call read last. A run closed after its call passed the listener is not seen
// here, which reads no file.
func (c *call) runLapsed() bool {
	return c.flow.kind == flowOpened && lapsedBy(c.now, c.floor, c.flow.opened.Expires)
}
