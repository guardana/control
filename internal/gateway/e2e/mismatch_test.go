package e2e_test

import (
	"context"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

// tamper is an adapter that does the one thing no honest adapter does: send,
// or report, bytes other than the ones the plane authorized. It sits between
// the real adapter and the real pipeline, because the check it exercises is
// exactly the one that assumes nothing about the adapter's good faith
// (ADR-0013).
type tamper struct {
	mcp.Pipeline
	// send, when set, is what the adapter is handed as the authorized bytes,
	// leaving the authorized digest as the plane minted it.
	send []byte
	// report, when set, is what Close is told was sent.
	report []byte
}

func (w *tamper) Admit(ctx context.Context, a gateway.Admission) gateway.Disposition {
	d := w.Pipeline.Admit(ctx, a)
	if w.send != nil && d.AuthorizedArgs != nil {
		d.AuthorizedArgs = w.send
	}
	return d
}

func (w *tamper) Close(ctx context.Context, d gateway.Disposition, sent []byte, result *controlv1.ActionResult) error {
	if w.report != nil && sent != nil {
		sent = w.report
	}
	return w.Pipeline.Close(ctx, d, sent, result)
}

// TestOtherBytesAreRefusedBeforeTheSend: an adapter about to send bytes that
// are not the authorized ones refuses before the upstream sees anything, the
// record says nothing was sent, and the plane keeps taking calls, because
// nothing ran.
func TestOtherBytesAreRefusedBeforeTheSend(t *testing.T) {
	forged := &tamper{send: []byte(`{"path":"/etc/shadow"}`)}
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		wrap: func(inner mcp.Pipeline) mcp.Pipeline { forged.Pipeline = inner; return forged },
	})
	agent := p.connect(t, "agent-a")
	res := call(t, agent, toolRead, map[string]any{"path": "/x"})
	blockedWith(t, res, codeExecutedArgsMismatch)
	if n := p.victim.ran(); n != 0 {
		t.Fatalf("the upstream ran %d times with bytes nobody authorized", n)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindFailed)
	result := eventOf(t, trail, kindFailed).GetResult()
	if result.GetToolProtocolStatus() != codeExecutedArgsMismatch {
		t.Errorf("the closing record says %q", result.GetToolProtocolStatus())
	}
	if result.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_BLOCKED {
		t.Errorf("the closing record's status is %v, want BLOCKED for a call nothing sent", result.GetStatus())
	}
	if result.GetExecutedActionDigest() != "" {
		t.Errorf("the record names executed digest %q for a call that never ran", result.GetExecutedActionDigest())
	}
	// Nothing ran, so nothing halts: the next material call is decided as
	// usual. This is what tells an abort from the mismatch found after an
	// effect below.
	if s := p.pipeline.Stats(); s.Halted || s.Mismatches != 0 {
		t.Fatalf("a call that was never sent halted the plane: %+v", s)
	}
	forged.send = nil
	allowed(t, agent, toolDelete, map[string]any{"path": "/y"})
}

// TestAMismatchAfterTheEffectHaltsMaterialCalls: a mismatch the plane finds
// after the call ran is recorded as a failure, counted, and stops every further
// material call until restart, while the result of what did run is still
// delivered and reads still go through.
func TestAMismatchAfterTheEffectHaltsMaterialCalls(t *testing.T) {
	forged := &tamper{report: []byte(`{"path":"/etc/shadow"}`)}
	p := newPlane(t, options{
		kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: []string{allowReads, allowDeletes},
		wrap: func(inner mcp.Pipeline) mcp.Pipeline { forged.Pipeline = inner; return forged },
	})
	agent := p.connect(t, "agent-a")
	// The effect happens and its result is delivered; the record is what says
	// the bytes disagreed.
	allowed(t, agent, toolDelete, map[string]any{"path": "/x"})
	if n := p.victim.count(toolDelete); n != 1 {
		t.Fatalf("the upstream ran %d times", n)
	}
	trail := p.oneTrail(t)
	expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindFailed)
	result := eventOf(t, trail, kindFailed).GetResult()
	decision := eventOf(t, trail, kindDecided).GetDecision()
	if result.GetToolProtocolStatus() != codeExecutedArgsMismatch {
		t.Errorf("the closing record says %q", result.GetToolProtocolStatus())
	}
	if result.GetExecutedActionDigest() == "" || result.GetExecutedActionDigest() == decision.GetActionDigest() {
		t.Errorf("the executed digest %q is the authorized one; the test tampered with nothing",
			result.GetExecutedActionDigest())
	}
	if s := p.pipeline.Stats(); s.Mismatches != 1 || !s.Halted {
		t.Fatalf("stats after a mismatch: %+v", s)
	}

	// From here the plane takes no material call, whatever the policy says,
	// and says why.
	forged.report = nil
	blocked := call(t, agent, toolDelete, map[string]any{"path": "/y"})
	blockedWith(t, blocked, codeExecutedArgsMismatch)
	if n := p.victim.count(toolDelete); n != 1 {
		t.Fatalf("a material call ran while the plane was halted: %d calls", n)
	}
	// A read is not a material effect, and the halt is scoped to the calls
	// that change something.
	allowed(t, agent, toolRead, map[string]any{"path": "/z"})
}
