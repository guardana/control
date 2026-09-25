package gateway_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/pause"
)

// TestAPauseWrittenDuringTheAskBitesThatCall: the state a call is decided
// under after the ask is the one read after it, at the clock read after it.
// With nothing changing during the ask the write runs, so each block below is
// the change's.
func TestAPauseWrittenDuringTheAskBitesThatCall(t *testing.T) {
	rules := []string{allowReads, allowWrites, vetoWrites}
	global := paused(t, pauseGlobal)
	for _, c := range []struct {
		name   string
		during func(h *harness)
		want   []string
	}{
		{"nothing changes", func(*harness) {}, nil},
		{"a global pause", func(h *harness) { h.pause.set(global) }, []string{codePaused}},
		{"one tick past three intervals", func(h *harness) {
			h.clock.set(base().Add(3*pauseInterval + time.Nanosecond))
		}, []string{codePauseStateUnavailable}},
		{"both at once", func(h *harness) {
			h.pause.set(global)
			h.clock.set(base().Add(5 * pauseInterval))
		}, []string{codePauseStateUnavailable}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dp := &fakeDecisionPoint{answer: core.ExternalAllowed()}
			h := build(t, modeEnforce, snapshot(t, rules...), withDecisionPoint(dp))
			h.pause.set(paused(t))
			dp.during = func() { c.during(h) }
			d := h.admit(tool(writeEnvelope()), []byte(`{"k":1}`))
			if len(dp.questions()) != 1 {
				t.Fatalf("the decision point was asked %d time(s), want once", len(dp.questions()))
			}
			if c.want == nil {
				if d.Action != core.Execute {
					t.Fatalf("with nothing changed during the ask: Action = %d, codes %v", d.Action, d.Decision.GetReasonCodes())
				}
				return
			}
			verdict := verdictDeny
			if c.want[0] == codePauseStateUnavailable {
				verdict = verdictIndeterminate
			}
			expectPlaneBlock(t, h, d, verdict, c.want...)
			expectConsulted(t, h.recorded(t, "req-1"), verdictAllow, codePDPAllow)
		})
	}
}

// sequence serves its snapshots in order, one per read, the last repeating,
// and counts the reads.
type sequence struct {
	snaps []pause.Snapshot
	reads atomic.Int64
}

func (s *sequence) Current() pause.Snapshot {
	i := int(s.reads.Add(1)) - 1
	return s.snaps[min(i, len(s.snaps)-1)]
}

// TestAfterTheAskOneStateDecidesTheRest: on the rewrite path with a decision
// point, the call reads the pause state once before the ask, once after it,
// and once more right before it starts, and decides the rewritten call under
// the second read: a read before the second decision would make four.
func TestAfterTheAskOneStateDecidesTheRest(t *testing.T) {
	clearState, payments := paused(t), paused(t, pausePayments)
	for _, c := range []struct {
		name         string
		snaps        []pause.Snapshot
		reads, asks  int
		blockedPause bool
	}{
		{"paused before the ask", []pause.Snapshot{payments}, 1, 0, true},
		{"paused during the ask", []pause.Snapshot{clearState, payments}, 2, 1, true},
		{"clear throughout", []pause.Snapshot{clearState, clearState, clearState}, 3, 1, false},
		{"paused before the start", []pause.Snapshot{clearState, clearState, payments}, 3, 1, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := &sequence{snaps: c.snaps}
			dp := &fakeDecisionPoint{answer: core.ExternalAllowed()}
			h := build(t, modeEnforce, snapshot(t, cappedRefunds, vetoRefunds), withDecisionPoint(dp),
				func(cfg *gateway.Config) { cfg.Pause = src })
			d := h.admit(tool(refundEnvelope(t, refundArgs())), refundArgs())
			if got := src.reads.Load(); got != int64(c.reads) {
				t.Errorf("the call read the pause source %d time(s), want %d", got, c.reads)
			}
			if got := len(dp.questions()); got != c.asks {
				t.Errorf("the decision point was asked %d time(s), want %d", got, c.asks)
			}
			switch {
			case c.blockedPause:
				expectPlaneBlock(t, h, d, verdictDeny, codePaused)
			case d.Action != core.ExecuteWithObligations:
				t.Errorf("under clear reads: Action = %d, codes %v; want the capped refund run", d.Action, d.Decision.GetReasonCodes())
			}
		})
	}
}

// armed runs hook once, on the first id drawn after arm.
type armed struct {
	next  func() string
	hook  func()
	armed atomic.Bool
}

func (a *armed) newID() string {
	if a.armed.CompareAndSwap(true, false) {
		a.hook()
	}
	return a.next()
}

// TestAHaltThatLiftsAfterThePlaneNamedItStillBlocks: the causes are named
// once, before any ask. The evidence halt stands when the write is admitted,
// so nobody is asked; a read that lifts the halt while the kernel decides the
// write does not let the write through with a decision nobody asked about.
func TestAHaltThatLiftsAfterThePlaneNamedItStillBlocks(t *testing.T) {
	dp := &fakeDecisionPoint{answer: core.ExternalAllowed()}
	var ids *armed
	h := build(t, modeEnforce, snapshot(t, allowReads, allowWrites, vetoWrites), withDecisionPoint(dp),
		func(c *gateway.Config) {
			ids = &armed{next: c.NewID}
			c.NewID = ids.newID
		})
	haltOnSink(t, h)
	h.sink.mu.Lock()
	clear(h.sink.refuse)
	h.sink.mu.Unlock()
	if !h.p.Stats().Halted {
		t.Fatal("setup: the evidence halt did not stand")
	}
	ids.hook = func() {
		if d := h.admit(requestNamed(tool(readEnvelope()), "lifts"), []byte(`{}`)); d.Action != core.Execute {
			t.Errorf("the read that lifts the halt: Action = %d, codes %v", d.Action, d.Decision.GetReasonCodes())
		}
	}
	ids.armed.Store(true)
	d := h.admit(requestNamed(tool(writeEnvelope()), "req-x"), []byte(`{"k":1}`))
	if h.p.Stats().Halted {
		t.Fatal("the halt did not lift while the write was decided, so this test examined nothing")
	}
	expectPlaneBlock(t, h, d, verdictIndeterminate, codeEvidenceUnavailable)
	if n := len(dp.questions()); n != 0 {
		t.Errorf("the decision point was asked %d time(s) about a call the plane blocked", n)
	}
	expectNotAsked(t, h.recorded(t, "req-x"))
}

// TestAHaltThatAppearsDuringTheAskBlocks: with no cause before the ask the
// write is asked about; a mismatch that halts the plane while the ask waits
// is named after it, and the write is blocked on it.
func TestAHaltThatAppearsDuringTheAskBlocks(t *testing.T) {
	dp := &fakeDecisionPoint{answer: core.ExternalAllowed()}
	h := build(t, modeEnforce, snapshot(t, allowReads, allowWrites, vetoWrites), withDecisionPoint(dp))
	read := h.admit(requestNamed(tool(readEnvelope()), "halts"), []byte(`{"q":1}`))
	if read.Action != core.Execute {
		t.Fatalf("setup: the read is %d, codes %v", read.Action, read.Decision.GetReasonCodes())
	}
	dp.during = func() {
		_ = h.p.Close(context.Background(), read, []byte(`{"q":2}`), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS))
	}
	d := h.admit(requestNamed(tool(writeEnvelope()), "req-x"), []byte(`{"k":1}`))
	if n := len(dp.questions()); n != 1 {
		t.Fatalf("the decision point was asked %d time(s), want once", n)
	}
	expectPlaneBlock(t, h, d, verdictDeny, codeExecutedArgsMismatch)
	expectConsulted(t, h.recorded(t, "req-x"), verdictAllow, codePDPAllow)
}

// publishing is a pause source whose every read publishes a fresh read of a
// clear file, dated at a clock that moves on by a tick at each read: a poll
// that lands between the call's reading of the clock and of the state.
type publishing struct {
	clock *clock
	path  string
}

func (s *publishing) Current() pause.Snapshot {
	now := s.clock.read().Add(time.Nanosecond)
	s.clock.set(now)
	return pause.Read(s.path, now, pauseInterval)
}

// TestTheStateIsTakenBeforeTheClock: a snapshot published just before the
// call reads the clock is never dated ahead of the call.
func TestTheStateIsTakenBeforeTheClock(t *testing.T) {
	var h *harness
	src := &publishing{path: pauseFile(t)}
	h = build(t, modeEnforce, snapshot(t, allowReads), func(c *gateway.Config) { c.Pause = src })
	src.clock = h.clock
	for i := range 3 {
		d := h.admit(requestNamed(tool(readEnvelope()), fmt.Sprintf("req-%d", i)), []byte(`{}`))
		if d.Action != core.Execute {
			t.Errorf("call %d under a clear state read just now: Action = %d, codes %v", i, d.Action, d.Decision.GetReasonCodes())
		}
	}
}

// TestAPauseCancelsNothingAlreadyRunning: an execution handed out before a
// global pause closes as it would have: ACTION_COMPLETED is recorded and the
// result is the caller's to deliver.
func TestAPauseCancelsNothingAlreadyRunning(t *testing.T) {
	h := build(t, modeEnforce, snapshot(t, allowReads))
	h.pause.set(paused(t))
	d := h.admit(tool(readEnvelope()), []byte(`{}`))
	if d.Action != core.Execute {
		t.Fatalf("setup: Action = %d, codes %v", d.Action, d.Decision.GetReasonCodes())
	}
	h.pause.set(paused(t, pauseGlobal))
	if blocked := h.admit(requestNamed(tool(readEnvelope()), "req-2"), []byte(`{}`)); blocked.Action != core.Block {
		t.Fatalf("the pause does not stand: Action = %d", blocked.Action)
	}
	if err := h.p.Close(context.Background(), d, []byte(`{}`), result(controlv1.ResultStatus_RESULT_STATUS_SUCCESS)); err != nil {
		t.Fatalf("Close under a global pause: %v", err)
	}
	trail := h.trailOf("req-1")
	expectKinds(t, kindsOf(trail), []controlv1.EventKind{kindProposed, kindDecided, kindStarted, kindCompleted})
	if err := evidence.ValidateChain(trail); err != nil {
		t.Errorf("ValidateChain: %v", err)
	}
	if s := h.p.Stats(); s.Open != 0 || s.Halted {
		t.Errorf("Stats after the close: open %d, halted %v", s.Open, s.Halted)
	}
}
