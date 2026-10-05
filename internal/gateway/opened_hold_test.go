package gateway_test

import (
	"sync"
	"testing"
	"time"

	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

// ticking is a clock that moves forward on every reading, as a real one does
// between a call's first reading and the kernel's.
func ticking(start time.Time) func() time.Time {
	var mu sync.Mutex
	now := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(time.Millisecond)
		return now
	}
}

// TestACallUnderAnOpenedRunIsHeldOnAClockThatMoves: a call that needs an
// approval under an opened run is held, its approval answered and its retry
// run, when the clock reads later at every step, as it does on a live plane.
func TestACallUnderAnOpenedRunIsHeldOnAClockThatMoves(t *testing.T) {
	runs := newRuns("root-1")
	h := build(t, modeEnforce, snapshot(t, approveRefunds), withRuns(runs), func(c *gateway.Config) { c.Clock = ticking(base()) })
	run := opened("run-a", "root-1")
	first := h.admitA(under(admission(refundEnvelope(t, refundArgs()), refundArgs()), run))
	if first.Action != core.AwaitApproval || first.Pending == nil {
		t.Fatalf("the refund under an opened run: Action = %d, %s %v; want it held", first.Action, first.Decision.GetVerdict(), first.Decision.GetReasonCodes())
	}
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base().Add(time.Minute)); err != nil {
		t.Fatalf("answering the approval: %v", err)
	}
	again := h.admitA(under(admission(retry(t, first.Decision.GetRequestId()), refundArgs()), run))
	if again.Action != core.ExecuteWithObligations && again.Action != core.Execute {
		t.Errorf("the approved retry: Action = %d, %s %v; want it run", again.Action, again.Decision.GetVerdict(), again.Decision.GetReasonCodes())
	}
}

// scripted is a clock whose n-th reading the test chooses; it counts the
// readings, so a test that relies on which reading approve() takes fails
// loudly once that number changes instead of checking another reading.
type scripted struct {
	mu sync.Mutex
	n  int
	at func(n int) time.Time
}

func (s *scripted) read() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return s.at(s.n)
}

// approveReading is which reading of the clock approve() takes when a first
// call under an opened run reaches it: the call's own, the kernel's, and the
// one approve() takes afresh last.
const approveReading = 6

// TestARunThatLapsesBeforeTheHoldIsNotHeld: a run alive at the call's first
// reading and the kernel's, and expired, or not vouched for, at the reading
// approve() takes, holds nothing.
func TestARunThatLapsesBeforeTheHoldIsNotHeld(t *testing.T) {
	ms := func(n int) time.Time { return base().Add(time.Duration(n) * time.Millisecond) }
	for name, c := range map[string]struct{ expires, last time.Time }{
		"expired between the kernel's reading and approve's": {ms(approveReading - 1), ms(approveReading)},
		"expired exactly at approve's reading":               {ms(approveReading), ms(approveReading)},
		"approve's reading behind the kernel's":              {base().Add(time.Hour), ms(approveReading - 3)},
		"approve's reading after 9999":                       {base().Add(time.Hour), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
		"approve's reading zero":                             {base().Add(time.Hour), time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			s := &scripted{at: func(n int) time.Time {
				if n == approveReading {
					return c.last
				}
				return ms(n)
			}}
			h := build(t, modeEnforce, snapshot(t, approveRefunds), withRuns(newRuns("root-1")), func(cf *gateway.Config) { cf.Clock = s.read })
			run := opened("run-a", "root-1")
			run.Expires = c.expires
			d := h.admitA(under(admission(refundEnvelope(t, refundArgs()), refundArgs()), run))
			if s.n != approveReading {
				t.Fatalf("the call read the clock %d times, want %d; re-count which reading approve() takes", s.n, approveReading)
			}
			if d.Action != core.Block || d.Pending != nil || h.p.Stats().Held != 0 {
				t.Errorf("Action = %d, pending %+v, held %d; want a block and nothing held", d.Action, d.Pending, h.p.Stats().Held)
			}
		})
	}
}

// TestAClockSetBackAfterApprovesReadingSpendsNothing: approve() relies on its
// fresh reading, so a later reading behind it, though after the kernel's and
// the hold's, vouches for nothing and the approval is not spent.
func TestAClockSetBackAfterApprovesReadingSpendsNothing(t *testing.T) {
	retrying := false
	s := &scripted{}
	s.at = func(n int) time.Time {
		if retrying && n == 2*approveReading {
			return base().Add(30 * time.Second)
		}
		return base().Add(time.Duration(n) * time.Millisecond)
	}
	h := build(t, modeEnforce, snapshot(t, approveRefunds), withRuns(newRuns("root-1")), func(cf *gateway.Config) { cf.Clock = s.read })
	run := opened("run-a", "root-1")
	first := h.admitA(under(admission(refundEnvelope(t, refundArgs()), refundArgs()), run))
	if first.Pending == nil || s.n != approveReading {
		t.Fatalf("the first call: pending %+v after %d readings, want held after %d", first.Pending, s.n, approveReading)
	}
	if err := h.store.Answer(first.Pending.ApprovalID, approved, "alice", "", base().Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	retrying = true
	again := h.admitA(under(admission(retry(t, "req-2"), refundArgs()), run))
	if again.Action == core.Execute || again.Action == core.ExecuteWithObligations {
		t.Errorf("the retry ran on a clock set back after approve()'s reading")
	}
}
