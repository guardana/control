package supervise_test

import (
	"slices"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/supervise"
	"google.golang.org/protobuf/proto"
)

// landLate is x with the last event of request req moved before its first,
// as a collector writes the trail when the plane's later batch lands first.
func landLate(t *testing.T, x supervise.Export, req string) supervise.Export {
	t.Helper()
	first, last := -1, -1
	for i, ev := range x.Events {
		if ev.GetRequestId() == req {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 || first == last {
		t.Fatalf("request %s holds no two events to reorder", req)
	}
	evs := slices.Clone(x.Events)
	moved := evs[last]
	copy(evs[first+1:last+1], x.Events[first:last])
	evs[first] = moved
	return supervise.Export{Whole: x.Whole, Events: evs}
}

// TestATrailLandedOutOfOrderIsJudgedByItsLinks: a request whose events an
// export holds out of their order, each linked to the one before, is one
// coherent trail and confirms as it does in order; a batch shipped twice is
// one copy left out.
func TestATrailLandedOutOfOrderIsJudgedByItsLinks(t *testing.T) {
	t.Run("a denial under 0.1", func(t *testing.T) {
		denied := landLate(t, export(denials(4)...), "d2")
		if denied.Events[7].GetEventId() != "d2-e3" || denied.Events[8].GetEventId() != "d2-e1" {
			t.Fatalf("the late landing holds %s, %s where d2 begins", denied.Events[7].GetEventId(), denied.Events[8].GetEventId())
		}
		got := only(closedRun(t, procWith(t), denied), "REPEATED_DENIAL")
		if len(got) != 1 || got[0].GetFinding().GetVerdict() != confirmed || citedEvents(got[0]) != "d1-e3 d2-e3 d3-e3 d4-e3" {
			t.Fatalf("REPEATED_DENIAL is %d findings, want one confirmed on the four blocks:\n%s", len(got), dump(got))
		}
	})
	t.Run("a resource under 0.2", func(t *testing.T) {
		res := supervise02(t, family(), landLate(t, acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("43"))), "r1"))
		names(t, res, ruleResource, runID, id02(ruleResource, "order", runID))
		if f := only(res, ruleResource)[0]; f.GetFinding().GetVerdict() != confirmed || citedEvents(f) != "r1-e1 r2-e1" {
			t.Fatalf("%s is %s citing %q, want confirmed citing r1-e1 r2-e1", ruleResource, f.GetFinding().GetVerdict(), citedEvents(f))
		}
	})
	t.Run("a late batch shipped again", func(t *testing.T) {
		x := landLate(t, acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("43"))), "r1")
		x.Events = append(x.Events, proto.Clone(x.Events[0]).(*controlv1.Event))
		res := supervise02(t, family(), x)
		if got := verdicts(res, ruleResource); !slices.Equal(got, []string{runID + " FINDING_VERDICT_CONFIRMED"}) ||
			res.Report.GetRead().GetEventsLeftOut()["duplicate"] != 1 {
			t.Fatalf("findings %q, events left out %v; want the run's confirmed and one duplicate:\n%s",
				got, res.Report.GetRead().GetEventsLeftOut(), dump(res.Findings))
		}
	})
}

// TestATrailWhoseLinksMakeNoChainStaysInDoubtInAnyOrder: links that leave an
// event out of the one chain, a second event on one link, a missing link, a
// second head or a cycle beside the chain, put the trail in doubt however it
// landed, so the id only it carries confirms nothing.
func TestATrailWhoseLinksMakeNoChainStaysInDoubtInAnyOrder(t *testing.T) {
	trails := func(edit func([]*controlv1.Event) []*controlv1.Event) supervise.Export {
		x := acts(lookupOf("r1", 0, orderNo("42")), refundOf("r2", 10*time.Second, orderNo("43")))
		x.Events = append(edit(x.Events[:4:4]), x.Events[4:]...)
		return landLate(t, x, "r1")
	}
	again := func(ev *controlv1.Event, id, prev string) *controlv1.Event {
		c := proto.Clone(ev).(*controlv1.Event)
		c.EventId, c.PrevEventId = id, prev
		return c
	}
	for name, x := range map[string]supervise.Export{
		"a second event on one link": trails(func(evs []*controlv1.Event) []*controlv1.Event {
			return append(evs, again(evs[3], "r1-e9", "r1-e3"))
		}),
		"a missing link": trails(func(evs []*controlv1.Event) []*controlv1.Event {
			return slices.Delete(evs, 1, 2)
		}),
		"a second head": trails(func(evs []*controlv1.Event) []*controlv1.Event {
			return append(evs, again(evs[0], "r1-e9", ""))
		}),
		"a cycle beside the chain": trails(func(evs []*controlv1.Event) []*controlv1.Event {
			return append(evs, again(evs[2], "r1-e8", "r1-e9"), again(evs[3], "r1-e9", "r1-e8"))
		}),
	} {
		t.Run(name, func(t *testing.T) {
			res := supervise02(t, family(), x)
			if got := verdicts(res, ruleResource); !slices.Equal(got, []string{runID + " FINDING_VERDICT_INDETERMINATE"}) {
				t.Fatalf("findings %q, want the run's indeterminate:\n%s", got, dump(res.Findings))
			}
		})
	}
}
