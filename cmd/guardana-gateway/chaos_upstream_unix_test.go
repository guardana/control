//go:build unix

package main

import (
	"path/filepath"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestAStdioUpstreamThatDiesMidCallIsNeverASuccess runs the built gateway with
// a stdio upstream, a child process that exits while a material call to it is
// in flight, and an HTTP upstream beside it. The agent gets an error, the
// trail closes the call as failed and never completed, the child received the
// call once and was never started again, the other upstream keeps serving, and
// every later call to the dead one is an error recorded the same way.
func TestAStdioUpstreamThatDiesMidCallIsNeverASuccess(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "stdio-upstream.log")
	collector := newAcceptingCollector(t)
	orders := newHTTPUpstream(t, answerEvery, toolReadOrder)
	plane := startChaosPlane(t, dir, chaosSetup{
		document: chaosDocument, collector: collector.url,
		upstreams: []chaosUpstream{{name: "orders", endpoint: orders.url}, stdioHelper(t, "notes", log)},
		tools: []classified{
			{"orders", toolReadOrder, "READ"},
			{"notes", toolReadNote, "READ"},
			{"notes", toolWriteNote, "WRITE"},
		},
	})
	agent := connectAgent(t, plane.listen)

	res, err := chaosCall(t, agent, toolReadNote, "n-1")
	ran(t, res, err, "a read of the live stdio upstream")

	res, err = chaosCall(t, agent, toolWriteNote, "n-2")
	if err == nil {
		t.Fatalf("a write whose upstream died mid-call came back as a result: %+v", res)
	}
	if got := recorded(t, log, toolWriteNote.Name); got != 1 {
		t.Fatalf("the stdio upstream received the write %d time(s), want exactly once", got)
	}

	res, err = chaosCall(t, agent, toolReadOrder, "o-1")
	ran(t, res, err, "a read of the other upstream after the stdio upstream died")

	res, err = chaosCall(t, agent, toolReadNote, "n-3")
	if err == nil {
		t.Fatalf("a read of the dead stdio upstream came back as a result: %+v", res)
	}
	if got := recorded(t, log, "start"); got != 1 {
		t.Errorf("the stdio upstream was started %d time(s); nothing may start it again", got)
	}
	if got := recorded(t, log, toolWriteNote.Name) + recorded(t, log, toolReadNote.Name); got != 2 {
		t.Errorf("the stdio upstream received %d call(s), want the read and the write", got)
	}
	if got := orders.count(toolReadOrder.Name); got != 1 {
		t.Errorf("the HTTP upstream received %d read(s), want 1", got)
	}

	collector.waitUntil(t, "four closed trails", allClosed(4))
	events := collector.events(t)
	validChains(t, events)
	died := endedUnfinished(t, trailOfTool(t, events, toolWriteNote.Name, "n-2"), "the write in flight")
	if died.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN {
		t.Errorf("the write in flight is closed as %v, want UNKNOWN: nobody knows whether it took effect", died.GetStatus())
	}
	after := endedUnfinished(t, trailOfTool(t, events, toolReadNote.Name, "n-3"), "the read after the death")
	if after.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN {
		t.Errorf("the read after the death is closed as %v, want UNKNOWN", after.GetStatus())
	}
}

// TestAnHTTPUpstreamThatDropsTheCallIsNeverASuccess runs the built gateway
// with an HTTP upstream that reads a material call and drops the connection,
// before its status line or after it, and a second upstream beside it. The
// agent gets an error, the trail closes the call as failed with an unknown
// result, the upstream received it once, the other upstream keeps serving, and
// the next call to the same upstream is sent and answered.
func TestAnHTTPUpstreamThatDropsTheCallIsNeverASuccess(t *testing.T) {
	for _, c := range []struct {
		name string
		drop dropKind
	}{
		{"before the status line", dropBeforeStatus},
		{"after the status line", dropAfterStatus},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			collector := newAcceptingCollector(t)
			orders := newHTTPUpstream(t, answerEvery, toolReadOrder)
			ledger := newHTTPUpstream(t, func(tool string, n int) dropKind {
				if tool == toolPostEntry.Name && n == 1 {
					return c.drop
				}
				return answerCall
			}, toolPostEntry)
			plane := startChaosPlane(t, dir, chaosSetup{
				document: chaosDocument, collector: collector.url,
				upstreams: []chaosUpstream{{name: "orders", endpoint: orders.url}, {name: "ledger", endpoint: ledger.url}},
				tools: []classified{
					{"orders", toolReadOrder, "READ"},
					{"ledger", toolPostEntry, "WRITE"},
				},
			})
			agent := connectAgent(t, plane.listen)

			res, err := chaosCall(t, agent, toolPostEntry, "e-1")
			if err == nil {
				t.Fatalf("a write whose connection dropped came back as a result: %+v", res)
			}
			if got := ledger.count(toolPostEntry.Name); got != 1 {
				t.Fatalf("the upstream received the dropped write %d time(s), want exactly once", got)
			}

			res, err = chaosCall(t, agent, toolReadOrder, "o-1")
			ran(t, res, err, "a read of the other upstream after the drop")

			res, err = chaosCall(t, agent, toolPostEntry, "e-2")
			ran(t, res, err, "the next write to the upstream that dropped one")
			if got := ledger.count(toolPostEntry.Name); got != 2 {
				t.Errorf("the upstream received %d write(s) for two calls, want 2", got)
			}

			collector.waitUntil(t, "three closed trails", allClosed(3))
			events := collector.events(t)
			validChains(t, events)
			dropped := endedUnfinished(t, trailOfTool(t, events, toolPostEntry.Name, "e-1"), "the dropped write")
			next := trailOfTool(t, events, toolPostEntry.Name, "e-2")
			if last := next[len(next)-1].GetKind(); last != kCompleted {
				t.Errorf("the next write's trail ends %v, want ACTION_COMPLETED", last)
			}
			if dropped.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_UNKNOWN {
				t.Errorf("the dropped write is closed as %v %q, want UNKNOWN: nobody knows whether it took effect",
					dropped.GetStatus(), dropped.GetToolProtocolStatus())
			}
		})
	}
}
